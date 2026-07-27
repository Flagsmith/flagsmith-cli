package cmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/cache"
	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

// metaFanOut bounds how many per-feature metadata reads run concurrently.
const metaFanOut = 8

var flagCmd = &cobra.Command{
	Use:   "flag",
	Short: "Inspect feature flags in the current environment",
}

// flagView is the curated JSON/detail shape for a flag's environment state —
// state hoisted to the top, with the metadata the human view shows, and none
// of the raw features-endpoint noise. Human output and JSON stay in lockstep.
type flagView struct {
	Feature           string `json:"feature"`
	Type              string `json:"type"`
	Description       string `json:"description"`
	Enabled           bool   `json:"enabled"`
	Value             any    `json:"value"`
	SegmentOverrides  int    `json:"segment_overrides"`
	IdentityOverrides int    `json:"identity_overrides"`
	CodeReferences    int    `json:"code_references"`
	LifecycleStage    string `json:"lifecycle_stage"`
}

func newFlagView(f *api.Feature) flagView {
	return flagView{
		Feature:           f.Name,
		Type:              featureTypeLabel(f.Type),
		Description:       f.Description,
		Enabled:           flagEnabled(f.EnvironmentState),
		Value:             stateValue(f.EnvironmentState),
		SegmentOverrides:  f.NumSegmentOverrides,
		IdentityOverrides: identityOverrideCount(f.NumIdentityOverrides),
		CodeReferences:    f.CodeReferences(),
		LifecycleStage:    f.LifecycleStage,
	}
}

// segmentRef identifies a segment in the curated JSON shapes.
type segmentRef struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// display renders a segment as "name (id)", degrading to the bare id when the
// name is unknown.
func (s segmentRef) display() string {
	return label(s.Name, s.ID)
}

// segmentFlagView is the curated shape for a flag's state in one segment.
type segmentFlagView struct {
	Feature  string     `json:"feature"`
	Type     string     `json:"type"`
	Segment  segmentRef `json:"segment"`
	Priority int        `json:"priority"`
	Enabled  bool       `json:"enabled"`
	Value    any        `json:"value"`
}

func newSegmentFlagView(f *api.Feature, segment segmentRef, priority int) segmentFlagView {
	return segmentFlagView{
		Feature:  f.Name,
		Type:     featureTypeLabel(f.Type),
		Segment:  segment,
		Priority: priority,
		Enabled:  flagEnabled(f.SegmentState),
		Value:    stateValue(f.SegmentState),
	}
}

// segmentOverrideMeta reads the segment's name and the override's priority
// from the feature-segments endpoint, seeding the name cache on the way. A
// missing row (e.g. an override created a moment ago) degrades to the bare id
// and priority 0 rather than failing.
func segmentOverrideMeta(cmd *cobra.Command, cred *activeCredential, environmentID, featureID, segmentID int) (segmentRef, int, error) {
	fss, err := cred.client().FeatureSegments(cmd.Context(), environmentID, featureID)
	if err != nil {
		return segmentRef{}, 0, err
	}
	names := make(map[string]string, len(fss))
	for _, fs := range fss {
		names[strconv.Itoa(fs.Segment)] = fs.SegmentName
	}
	_ = cache.Merge(apiURL, &cache.Names{Segments: names}) // opportunistic (04 §3)
	for _, fs := range fss {
		if fs.Segment == segmentID {
			return segmentRef{ID: segmentID, Name: fs.SegmentName}, fs.Priority, nil
		}
	}
	return segmentRef{ID: segmentID}, 0, nil
}

func flagEnabled(fs *api.FeatureState) bool {
	return fs != nil && fs.Enabled
}

// stateValue returns a feature state's raw value, or nil.
func stateValue(fs *api.FeatureState) any {
	if fs == nil {
		return nil
	}
	return fs.Value
}

// boolState renders on/off.
func boolState(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}

// valueDisplay renders a value for the human views: "-" when unset.
func valueDisplay(v any) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(v)
}

// featureTypeLabel lower-cases the feature type for display (the API returns
// e.g. STANDARD / MULTIVARIATE).
func featureTypeLabel(t string) string {
	return strings.ToLower(t)
}

// identityOverrideCount treats a null count (Edge/Dynamo projects) as 0.
func identityOverrideCount(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

// valueDisplayMax bounds how wide a flag value is shown in the list table;
// values can be very large (JSON blobs, long strings).
const valueDisplayMax = 40

// truncateValue flattens a value to a single line (values are often multi-line
// JSON) and shortens it for table display, marking the cut.
func truncateValue(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= valueDisplayMax {
		return s
	}
	return string(r[:valueDisplayMax-1]) + "…"
}

// flagContext resolves the credential, project, and environment every flag
// command needs.
func flagContext(cmd *cobra.Command) (*projectContext, *activeCredential, int, api.Environment, error) {
	pc, err := applyContext(cmd)
	if err != nil {
		return nil, nil, 0, api.Environment{}, err
	}
	cred, err := resolveCredential(cmd.Context())
	if err != nil {
		return nil, nil, 0, api.Environment{}, err
	}
	projectID, err := resolveProjectID(cmd, pc, cred)
	if err != nil {
		return nil, nil, 0, api.Environment{}, err
	}
	env, err := resolveEnvironment(cmd, pc, cred, projectID)
	if err != nil {
		return nil, nil, 0, api.Environment{}, err
	}
	return pc, cred, projectID, env, nil
}

var (
	flagListSegmentFlag  string
	flagListFeatureFlag  string
	flagListIdentityFlag bool
)

var flagListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every flag in the current environment",
	Example: `  # flags in the current environment
  flagsmith flag list

  # only the flags overridden for a segment
  flagsmith flag list --segment 12

  # one feature's segment or identity overrides
  flagsmith flag list --feature onboarding
  flagsmith flag list --feature onboarding --identity`,
	RunE: func(cmd *cobra.Command, args []string) error {
		switch {
		case flagListSegmentFlag != "" && flagListFeatureFlag != "":
			return usageErrorf("--segment and --feature are mutually exclusive")
		case flagListIdentityFlag && flagListFeatureFlag == "":
			return usageErrorf("--identity only applies together with --feature")
		}
		_, cred, projectID, env, err := flagContext(cmd)
		if err != nil {
			return err
		}
		segmentID, err := optionalSegmentID(cmd, cred, projectID, flagListSegmentFlag)
		if err != nil {
			return err
		}
		if flagListFeatureFlag != "" {
			feature, err := requireFeature(cmd, cred, projectID, env, segmentID, flagListFeatureFlag)
			if err != nil {
				return err
			}
			if flagListIdentityFlag {
				return listFeatureIdentityOverrides(cmd, cred, env, projectID, feature)
			}
			return listFeatureSegmentOverrides(cmd, cred, env, feature)
		}
		features, err := cred.client().Features(cmd.Context(), projectID, env.ID, segmentID, "")
		if err != nil {
			return err
		}
		if segmentID != 0 {
			return listSegmentOverrides(cmd, cred, env, features, segmentID)
		}
		views := make([]flagView, len(features))
		for i := range features {
			views[i] = newFlagView(&features[i])
		}
		return renderList(cmd, views, "No flags.",
			[]string{"NAME", "TYPE", "STATE", "VALUE", "LIFECYCLE"},
			func(_ int, v flagView) []string {
				return []string{v.Feature, v.Type, boolState(v.Enabled), truncateValue(valueDisplay(v.Value)), lifecycleOrDash(v.LifecycleStage)}
			}, "flag", "flags")
	},
}

// listSegmentOverrides renders only the flags overridden for a segment,
// showing the override's state rather than the environment default.
//
// The feature-segments endpoint requires a feature filter, so the override
// metadata costs one read per overridden feature; the reads fan out
// concurrently and the names they carry merge into the cache once.
func listSegmentOverrides(cmd *cobra.Command, cred *activeCredential, env api.Environment, features []api.Feature, segmentID int) error {
	overridden := make([]*api.Feature, 0, len(features))
	for i := range features {
		if features[i].SegmentState != nil {
			overridden = append(overridden, &features[i])
		}
	}
	rows := make([][]api.FeatureSegment, len(overridden))
	g, ctx := errgroup.WithContext(cmd.Context())
	g.SetLimit(metaFanOut)
	for i, feature := range overridden {
		g.Go(func() error {
			fss, err := cred.client().FeatureSegments(ctx, env.ID, feature.ID)
			if err != nil {
				return err
			}
			rows[i] = fss
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return err
	}
	names := map[string]string{}
	views := make([]segmentFlagView, len(overridden))
	for i, feature := range overridden {
		// A missing row (e.g. an override created a moment ago) degrades to
		// the bare id and priority 0 rather than failing.
		segment := segmentRef{ID: segmentID}
		priority := 0
		for _, fs := range rows[i] {
			names[strconv.Itoa(fs.Segment)] = fs.SegmentName
			if fs.Segment == segmentID {
				segment.Name = fs.SegmentName
				priority = fs.Priority
			}
		}
		views[i] = newSegmentFlagView(feature, segment, priority)
	}
	if len(names) > 0 {
		_ = cache.Merge(apiURL, &cache.Names{Segments: names}) // opportunistic (04 §3)
	}
	return renderList(cmd, views, "No segment overrides.",
		[]string{"NAME", "TYPE", "STATE", "VALUE"},
		func(_ int, v segmentFlagView) []string {
			return []string{v.Feature, v.Type, boolState(v.Enabled), truncateValue(valueDisplay(v.Value))}
		}, "flag", "flags")
}

var (
	flagGetSegmentFlag    string
	flagGetIdentifierFlag string
)

// optionalSegmentID resolves a --segment reference (id or name) when given,
// or reports 0 for "not set".
func optionalSegmentID(cmd *cobra.Command, cred *activeCredential, projectID int, ref string) (int, error) {
	if ref == "" {
		return 0, nil
	}
	return resolveSegmentID(cmd, cred, projectID, ref)
}

var flagGetCmd = &cobra.Command{
	Use:   "get <feature>",
	Short: "Show a single flag's state in the current environment",
	Example: `  flagsmith flag get onboarding

  # a segment or identity override
  flagsmith flag get onboarding --segment 12
  flagsmith flag get onboarding --identifier user-123`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if flagGetSegmentFlag != "" && flagGetIdentifierFlag != "" {
			return usageErrorf("--segment and --identifier are mutually exclusive")
		}
		_, cred, projectID, env, err := flagContext(cmd)
		if err != nil {
			return err
		}
		segmentID, err := optionalSegmentID(cmd, cred, projectID, flagGetSegmentFlag)
		if err != nil {
			return err
		}
		feature, err := requireFeature(cmd, cred, projectID, env, segmentID, name)
		if err != nil {
			return err
		}
		if flagGetIdentifierFlag != "" {
			edge, err := useEdgeIdentities(cmd, cred, projectID)
			if err != nil {
				return err
			}
			override, err := readIdentityOverride(cmd, cred, env.APIKey, feature.ID, flagGetIdentifierFlag, edge)
			if err != nil {
				return err
			}
			if override == nil {
				return fmt.Errorf("%q has no override for identifier %q in %s", name, flagGetIdentifierFlag, environmentLabel(env))
			}
			return renderIdentityDetail(cmd, feature, flagGetIdentifierFlag, override)
		}
		if segmentID != 0 {
			if feature.SegmentState == nil {
				return fmt.Errorf("%q has no override for segment %d in %s", name, segmentID, environmentLabel(env))
			}
			v, err := buildSegmentFlagView(cmd, cred, env, feature, segmentID)
			if err != nil {
				return err
			}
			return renderSegmentDetail(cmd, v)
		}
		return renderFlagDetail(cmd, feature)
	},
}

// findFeature returns the feature whose name matches ref exactly (case
// insensitive), or nil. The features endpoint's own filter is a contains match.
func findFeature(features []api.Feature, ref string) *api.Feature {
	for i := range features {
		if strings.EqualFold(features[i].Name, ref) {
			return &features[i]
		}
	}
	return nil
}

// requireFeature fetches the environment's features narrowed by the ref (a
// server-side contains match on names; id refs fetch unfiltered) and requires
// exactly the referenced one.
func requireFeature(cmd *cobra.Command, cred *activeCredential, projectID int, env api.Environment, segmentID int, ref string) (*api.Feature, error) {
	features, err := cred.client().Features(cmd.Context(), projectID, env.ID, segmentID, nameRef(ref))
	if err != nil {
		return nil, err
	}
	f := findFeatureByRef(features, ref)
	if f == nil {
		return nil, withHint(
			fmt.Errorf("feature %q not found in %s", ref, environmentLabel(env)),
			hintFlagList)
	}
	return f, nil
}

// findFeatureByRef matches a feature by reference: all-digit → id, anything
// else → name (04 §3).
func findFeatureByRef(features []api.Feature, ref string) *api.Feature {
	id, err := strconv.Atoi(ref)
	if err != nil {
		return findFeature(features, ref)
	}
	for i := range features {
		if features[i].ID == id {
			return &features[i]
		}
	}
	return nil
}

// listFeatureSegmentOverrides renders one feature's segment overrides in
// priority order, joining state and value onto the feature-segment rows.
func listFeatureSegmentOverrides(cmd *cobra.Command, cred *activeCredential, env api.Environment, feature *api.Feature) error {
	fss, err := cred.client().FeatureSegments(cmd.Context(), env.ID, feature.ID)
	if err != nil {
		return err
	}
	states, err := cred.client().FeatureStates(cmd.Context(), env.ID, feature.ID)
	if err != nil {
		return err
	}
	stateByFS := make(map[int]api.EnvironmentFeatureState, len(states))
	for _, s := range states {
		if s.FeatureSegment != nil {
			stateByFS[*s.FeatureSegment] = s
		}
	}
	views := make([]segmentFlagView, len(fss)) // fss is already in priority order
	for i, fs := range fss {
		state := stateByFS[fs.ID]
		views[i] = segmentFlagView{
			Feature:  feature.Name,
			Type:     featureTypeLabel(feature.Type),
			Segment:  segmentRef{ID: fs.Segment, Name: fs.SegmentName},
			Priority: fs.Priority,
			Enabled:  state.Enabled,
			Value:    state.Value.Scalar(),
		}
	}
	return renderSegmentOverrideList(cmd, views)
}

// renderSegmentOverrideList prints segment-override views in priority order.
func renderSegmentOverrideList(cmd *cobra.Command, views []segmentFlagView) error {
	return renderList(cmd, views, "No segment overrides.",
		[]string{"PRIORITY", "SEGMENT", "STATE", "VALUE"},
		func(_ int, v segmentFlagView) []string {
			return []string{strconv.Itoa(v.Priority), v.Segment.display(), boolState(v.Enabled), truncateValue(valueDisplay(v.Value))}
		}, "override", "overrides")
}

// listFeatureIdentityOverrides renders one feature's identity overrides,
// branching core vs edge on the project's identity storage.
func listFeatureIdentityOverrides(cmd *cobra.Command, cred *activeCredential, env api.Environment, projectID int, feature *api.Feature) error {
	edge, err := useEdgeIdentities(cmd, cred, projectID)
	if err != nil {
		return err
	}
	var overrides []api.IdentityOverrideRow
	if edge {
		overrides, err = cred.client().EdgeIdentityOverrides(cmd.Context(), env.APIKey, feature.ID)
	} else {
		overrides, err = cred.client().CoreIdentityOverrides(cmd.Context(), env.APIKey, feature.ID)
	}
	if err != nil {
		return err
	}
	views := make([]identityFlagView, len(overrides))
	for i, o := range overrides {
		views[i] = identityFlagView{
			Feature:    feature.Name,
			Type:       featureTypeLabel(feature.Type),
			Identifier: o.Identifier,
			Enabled:    o.Enabled,
			Value:      o.Value,
		}
	}
	return renderList(cmd, views, "No identity overrides.",
		[]string{"IDENTIFIER", "STATE", "VALUE"},
		func(_ int, v identityFlagView) []string {
			return []string{v.Identifier, boolState(v.Enabled), truncateValue(valueDisplay(v.Value))}
		}, "override", "overrides")
}

// renderFlagDetail prints one flag's curated detail view (or its JSON).
func renderFlagDetail(cmd *cobra.Command, feature *api.Feature) error {
	v := newFlagView(feature)
	return output.Render(cmd.OutOrStdout(), v, outputOpts(), func(w io.Writer) error {
		return output.Detail(w, []output.Field{
			{Label: "Feature", Value: v.Feature},
			{Label: "Description", Value: v.Description},
			{Label: "Type", Value: v.Type},
			{Label: "State", Value: boolState(v.Enabled)},
			{Label: "Value", Value: valueDisplay(v.Value)},
			{Label: "Segment overrides", Value: strconv.Itoa(v.SegmentOverrides)},
			{Label: "Identity overrides", Value: strconv.Itoa(v.IdentityOverrides)},
			{Label: "Code references", Value: strconv.Itoa(v.CodeReferences)},
			{Label: "Lifecycle stage", Value: lifecycleOrDash(v.LifecycleStage)},
		})
	})
}

// buildSegmentFlagView resolves the override's segment name and priority and
// assembles the curated view — the fetch happens here, not in the renderer.
func buildSegmentFlagView(cmd *cobra.Command, cred *activeCredential, env api.Environment, feature *api.Feature, segmentID int) (segmentFlagView, error) {
	segment, priority, err := segmentOverrideMeta(cmd, cred, env.ID, feature.ID, segmentID)
	if err != nil {
		return segmentFlagView{}, err
	}
	return newSegmentFlagView(feature, segment, priority), nil
}

// renderSegmentDetail prints a flag's curated state for one segment override.
func renderSegmentDetail(cmd *cobra.Command, v segmentFlagView) error {
	return output.Render(cmd.OutOrStdout(), v, outputOpts(), func(w io.Writer) error {
		return output.Detail(w, []output.Field{
			{Label: "Feature", Value: v.Feature},
			{Label: "Type", Value: v.Type},
			{Label: "Segment", Value: v.Segment.display()},
			{Label: "Priority", Value: strconv.Itoa(v.Priority)},
			{Label: "State", Value: boolState(v.Enabled)},
			{Label: "Value", Value: valueDisplay(v.Value)},
		})
	})
}

func lifecycleOrDash(stage string) string {
	if stage == "" {
		return "-"
	}
	return stage
}

// environmentLabel renders an environment as "Name (key)" for messages.
func environmentLabel(env api.Environment) string {
	return label(env.Name, env.APIKey)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func init() {
	flagListCmd.Flags().StringVar(&flagListSegmentFlag, "segment", "", "list overrides for this segment (id or name)")
	flagListCmd.Flags().StringVar(&flagListFeatureFlag, "feature", "", "list this feature's segment overrides (id or name), in priority order")
	flagListCmd.Flags().BoolVar(&flagListIdentityFlag, "identity", false, "with --feature: list its identity overrides instead")
	flagGetCmd.Flags().StringVar(&flagGetSegmentFlag, "segment", "", "show the override for this segment (id or name)")
	flagGetCmd.Flags().StringVar(&flagGetIdentifierFlag, "identifier", "", "show the override for this identity")
	flagCmd.AddCommand(flagListCmd, flagGetCmd, flagUpdateCmd, flagEnableCmd, flagDisableCmd, flagReorderCmd, flagDeleteCmd, flagCreateCmd)
	rootCmd.AddCommand(flagCmd)
}
