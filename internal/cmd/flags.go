package cmd

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

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

// segmentFlagView is the curated shape for a flag's state in one segment.
type segmentFlagView struct {
	Feature string `json:"feature"`
	Type    string `json:"type"`
	Segment int    `json:"segment"`
	Enabled bool   `json:"enabled"`
	Value   any    `json:"value"`
}

func newSegmentFlagView(f *api.Feature, segmentID int) segmentFlagView {
	return segmentFlagView{
		Feature: f.Name,
		Type:    featureTypeLabel(f.Type),
		Segment: segmentID,
		Enabled: flagEnabled(f.SegmentState),
		Value:   stateValue(f.SegmentState),
	}
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

var flagListSegmentFlag int

var flagListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every flag in the current environment",
	Example: `  # flags in the current environment
  flagsmith flag list

  # only the flags overridden for a segment
  flagsmith flag list --segment 12`,
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cred, projectID, env, err := flagContext(cmd)
		if err != nil {
			return err
		}
		features, err := cred.client().Features(cmd.Context(), projectID, env.ID, flagListSegmentFlag)
		if err != nil {
			return err
		}
		if flagListSegmentFlag != 0 {
			return listSegmentOverrides(cmd, features, flagListSegmentFlag)
		}
		views := make([]flagView, len(features))
		for i := range features {
			views[i] = newFlagView(&features[i])
		}
		return output.Render(cmd.OutOrStdout(), views, outputOpts(), func(w io.Writer) error {
			if len(views) == 0 {
				fmt.Fprintln(w, "No flags.")
				return nil
			}
			rows := make([][]string, len(views))
			for i, v := range views {
				rows[i] = []string{
					v.Feature,
					v.Type,
					boolState(v.Enabled),
					truncateValue(valueDisplay(v.Value)),
					lifecycleOrDash(v.LifecycleStage),
				}
			}
			if err := output.Table(w, []string{"NAME", "TYPE", "STATE", "VALUE", "LIFECYCLE"}, rows); err != nil {
				return err
			}
			fmt.Fprintf(w, "\n%d %s\n", len(views), plural(len(views), "flag", "flags"))
			return nil
		})
	},
}

// listSegmentOverrides renders only the flags overridden for a segment,
// showing the override's state rather than the environment default.
func listSegmentOverrides(cmd *cobra.Command, features []api.Feature, segmentID int) error {
	views := []segmentFlagView{}
	for i := range features {
		if features[i].SegmentState != nil {
			views = append(views, newSegmentFlagView(&features[i], segmentID))
		}
	}
	return output.Render(cmd.OutOrStdout(), views, outputOpts(), func(w io.Writer) error {
		if len(views) == 0 {
			fmt.Fprintln(w, "No segment overrides.")
			return nil
		}
		rows := make([][]string, len(views))
		for i, v := range views {
			rows[i] = []string{v.Feature, v.Type, boolState(v.Enabled), truncateValue(valueDisplay(v.Value))}
		}
		if err := output.Table(w, []string{"NAME", "TYPE", "STATE", "VALUE"}, rows); err != nil {
			return err
		}
		fmt.Fprintf(w, "\n%d %s\n", len(views), plural(len(views), "flag", "flags"))
		return nil
	})
}

var (
	flagGetSegmentFlag    int
	flagGetIdentifierFlag string
)

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
		if flagGetSegmentFlag != 0 && flagGetIdentifierFlag != "" {
			return usageErrorf("--segment and --identifier are mutually exclusive")
		}
		_, cred, projectID, env, err := flagContext(cmd)
		if err != nil {
			return err
		}
		// The server's search is a contains match, so fetch and narrow to the
		// exact feature client-side.
		features, err := cred.client().Features(cmd.Context(), projectID, env.ID, flagGetSegmentFlag)
		if err != nil {
			return err
		}
		feature := findFeature(features, name)
		if feature == nil {
			return withHint(
				fmt.Errorf("feature %q not found in %s", name, environmentLabel(env)),
				hintFlagList)
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
		if flagGetSegmentFlag != 0 {
			if feature.SegmentState == nil {
				return fmt.Errorf("%q has no override for segment %d in %s", name, flagGetSegmentFlag, environmentLabel(env))
			}
			return renderSegmentDetail(cmd, feature, flagGetSegmentFlag)
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

// renderSegmentDetail prints a flag's curated state for one segment override.
func renderSegmentDetail(cmd *cobra.Command, feature *api.Feature, segmentID int) error {
	v := newSegmentFlagView(feature, segmentID)
	return output.Render(cmd.OutOrStdout(), v, outputOpts(), func(w io.Writer) error {
		return output.Detail(w, []output.Field{
			{Label: "Feature", Value: v.Feature},
			{Label: "Type", Value: v.Type},
			{Label: "Segment", Value: strconv.Itoa(v.Segment)},
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
	if env.Name != "" {
		return fmt.Sprintf("%s (%s)", env.Name, env.APIKey)
	}
	return env.APIKey
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func init() {
	flagListCmd.Flags().IntVar(&flagListSegmentFlag, "segment", 0, "list overrides for this segment id")
	flagGetCmd.Flags().IntVar(&flagGetSegmentFlag, "segment", 0, "show the override for this segment id")
	flagGetCmd.Flags().StringVar(&flagGetIdentifierFlag, "identifier", "", "show the override for this identity")
	flagCmd.AddCommand(flagListCmd, flagGetCmd, flagUpdateCmd, flagEnableCmd, flagDisableCmd, flagDeleteCmd, flagCreateCmd)
	rootCmd.AddCommand(flagCmd)
}
