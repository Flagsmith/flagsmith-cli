package cmd

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/api"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/bug"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/output"
)

var (
	flagEnableFlag       bool
	flagDisableFlag      bool
	flagValueFlag        string
	flagTypeFlag         string
	flagWeightFlags      []string
	flagUpdateSegment    string
	flagUpdateIdentifier string
	flagUpdatePriority   int
	flagDeleteSegment    string
	flagDeleteIdentifier string
)

var flagUpdateCmd = &cobra.Command{
	Use:   "update <feature>",
	Short: "Change a flag's state in the current environment",
	Example: `  # toggle the environment default
  flagsmith flag update onboarding --enable
  flagsmith flag update onboarding --disable

  # set a value (type inferred, or forced with --type)
  flagsmith flag update banner-text --value "Welcome!"
  flagsmith flag update max-items --value 10 --type integer

  # target a segment or identity override instead of the default
  flagsmith flag update onboarding --enable --segment 12
  flagsmith flag update onboarding --value beta --identifier user-123

  # re-weight a multivariate flag's variants (by key or id)
  flagsmith flag update banner-copy --weight hero=25 --weight sub=75
  flagsmith flag update banner-copy --segment 12 --weight hero=100,sub=0`,
	Args: cobra.ExactArgs(1),
	RunE: runFlagUpdate,
}

func runFlagUpdate(cmd *cobra.Command, args []string) error {
	m := flagMutation{
		enable:      cmd.Flags().Changed("enable"),
		disable:     cmd.Flags().Changed("disable"),
		setValue:    cmd.Flags().Changed("value"),
		setPriority: cmd.Flags().Changed("priority"),
		priority:    flagUpdatePriority,
		segmentRef:  flagUpdateSegment,
		identifier:  flagUpdateIdentifier,
	}
	switch {
	case m.segmentRef != "" && m.identifier != "":
		return usageErrorf("--segment and --identifier are mutually exclusive")
	case m.enable && m.disable:
		return usageErrorf("--enable and --disable are mutually exclusive")
	case m.setPriority && m.segmentRef == "":
		return usageErrorf("--priority only applies together with --segment")
	case len(flagWeightFlags) > 0 && m.identifier != "":
		return hintf(usageErrorf("--weight and --identifier are mutually exclusive"),
			"An identity is served one concrete value, not a distribution — use --value.")
	case !m.enable && !m.disable && !m.setValue && !m.setPriority && len(flagWeightFlags) == 0:
		return usageErrorf("nothing to update — pass --enable, --disable, --value, --weight, or --priority")
	case cmd.Flags().Changed("type") && !m.setValue:
		return usageErrorf("--type only applies together with --value")
	}
	// Parsed up front: bad syntax should cost no request.
	weights, err := parseWeights(flagWeightFlags)
	if err != nil {
		return err
	}
	m.weights = weights
	return applyFlagMutation(cmd, args[0], m)
}

// weightRef is one --weight pair: a variant reference (key or id) and the
// percentage of traffic it should serve.
type weightRef struct {
	ref    string
	weight float64
}

// controlVariantKey is the backend's reserved name for the share of traffic no
// variant claims. It is not a variant, so it cannot be weighted directly.
const controlVariantKey = "control"

// parseWeights parses repeated --weight values, each one or more
// comma-separated <key|id>=<percentage> pairs.
func parseWeights(args []string) ([]weightRef, error) {
	var refs []weightRef
	seen := map[string]bool{}
	for _, arg := range args {
		for _, pair := range strings.Split(arg, ",") {
			ref, raw, ok := strings.Cut(strings.TrimSpace(pair), "=")
			if !ok || ref == "" || raw == "" {
				return nil, usageErrorf("--weight %q is not <key|id>=<percentage>", pair)
			}
			weight, err := strconv.ParseFloat(raw, 64)
			if err != nil || weight < 0 || weight > 100 {
				return nil, usageErrorf("--weight %s: %q is not a percentage between 0 and 100", ref, raw)
			}
			if ref == controlVariantKey {
				return nil, hintf(usageErrorf("%q is not a variant", controlVariantKey),
					"Whatever the variants leave unallocated serves the flag's own value — set it with --value.")
			}
			// Two references to one variant are caught after they resolve, in
			// mergeWeights; this only catches the same one written twice.
			if seen[ref] {
				return nil, usageErrorf("--weight %s is given twice", ref)
			}
			seen[ref] = true
			refs = append(refs, weightRef{ref: ref, weight: weight})
		}
	}
	return refs, nil
}

// weightSummary renders the requested weights for a confirmation line, in the
// order and by the references the user gave.
func weightSummary(refs []weightRef) string {
	pairs := make([]string, len(refs))
	for i, r := range refs {
		pairs[i] = r.ref + "=" + formatWeight(r.weight)
	}
	return strings.Join(pairs, ", ")
}

// weightTolerance absorbs the rounding of percentages that are exact in decimal
// but not in binary, so weights a user reads as summing to 100 are accepted.
const weightTolerance = 1e-9

// mergeWeights overlays the requested weights onto the scope's current
// distribution, and returns the whole variant list: the endpoint rejects a
// partial one, and sending everything is also what keeps an unnamed variant at
// the weight it already had.
func mergeWeights(feature *api.Feature, current variantWeights, refs []weightRef) ([]api.Variant, error) {
	weights := make(variantWeights, len(feature.MultivariateOptions))
	for _, o := range feature.MultivariateOptions {
		weights[o.ID] = current[o.ID]
	}
	namedBy := make(map[int]string, len(refs))
	for _, r := range refs {
		// Resolved against the feature, never created: the endpoint would take
		// an unknown key as a new variant, so a typo would silently add one.
		option := findVariant(feature, r.ref)
		if option == nil {
			return nil, hintf(usageErrorf("%q is not a variant of %s", r.ref, feature.Name),
				"Run `flagsmith feature variant list %s` to see its variants, or `flagsmith feature variant add %s` to add one.",
				feature.Name, feature.Name)
		}
		// A variant named twice — by key and by id, say — is two weights for one
		// variant, and picking one of them silently is the wrong answer.
		if first, ok := namedBy[option.ID]; ok {
			return nil, usageErrorf("--weight %s and %s are the same variant", first, r.ref)
		}
		namedBy[option.ID] = r.ref
		weights[option.ID] = r.weight
	}
	total := 0.0
	variants := make([]api.Variant, 0, len(feature.MultivariateOptions))
	for _, o := range feature.MultivariateOptions {
		variants = append(variants, api.Variant{ID: o.ID, Weight: weights[o.ID]})
		total += weights[o.ID]
	}
	if total > 100+weightTolerance {
		return nil, usageErrorf("the merged weights add up to %s%%, over the 100%% available", formatWeight(total))
	}
	return variants, nil
}

// flagMutation is the state change a flag command applies to one feature — some
// combination of enable/disable/set-value, optionally scoped to a segment or
// identity override. `flag update` builds it from its flags; `flag enable` and
// `flag disable` are shorthands that preset it. segmentRef is an unresolved
// segment reference (id or name).
type flagMutation struct {
	enable, disable, setValue bool
	setPriority               bool
	priority                  int
	weights                   []weightRef
	segmentRef                string
	identifier                string
}

// applyFlagMutation resolves the feature and applies m as a partial update-flag
// write — only the properties m changes are sent, and the rest are left as they
// are — then reprints the resulting flag from the endpoint's response.
func applyFlagMutation(cmd *cobra.Command, name string, m flagMutation) error {
	_, cred, projectID, env, err := flagContext(cmd)
	if err != nil {
		return err
	}
	segmentID, err := optionalSegmentID(cmd, cred, projectID, m.segmentRef)
	if err != nil {
		return err
	}
	feature, err := requireFeature(cmd, cred, projectID, env, segmentID, name)
	if err != nil {
		return err
	}
	name = feature.Name // canonical for messages

	if m.identifier != "" {
		return runIdentityUpdate(cmd, cred, env, projectID, feature, m.identifier, m.enable, m.disable, m.setValue)
	}

	// The state being changed: the environment default, or a segment override.
	// The scope carries its own preposition: "in environment …" for the
	// default, "for segment name (id) in environment …" for an override.
	// The override's metadata (name, priority, feature-state link) is fetched
	// once and reused for the weights and the post-update render.
	target := feature.EnvironmentState
	scope := "in environment " + environmentLabel(env)
	var meta overrideMeta
	if segmentID != 0 {
		target = feature.SegmentState // nil when the override does not exist yet
		if meta, err = segmentOverrideMeta(cmd, cred, env.ID, feature.ID, segmentID); err != nil {
			return err
		}
		scope = fmt.Sprintf("for segment %s in environment %s", meta.segment.display(), environmentLabel(env))
	}

	// Priorities are a dense 0-based order; a new override joins it, growing the
	// valid range by one. The server treats the write as a move, so only the
	// bounds need checking.
	if m.setPriority {
		limit := feature.NumSegmentOverrides
		if target == nil {
			limit++
		}
		if m.priority < 0 || m.priority >= limit {
			return usageErrorf("--priority %d is out of range (0..%d)", m.priority, limit-1)
		}
	}

	state := api.FlagStateUpdate{}
	if m.enable || m.disable {
		state.Enabled = &m.enable
	}
	if m.setValue {
		value, err := inferFeatureValue(flagValueFlag, flagTypeFlag)
		if err != nil {
			return err
		}
		state.Value = &value
	}
	if len(m.weights) > 0 {
		if len(feature.MultivariateOptions) == 0 {
			return hintf(usageErrorf("%s has no variants to weight", name),
				"Add one with `flagsmith feature variant add %s --value <value>`.", name)
		}
		current, err := scopeWeights(cmd, cred, env.ID, feature, meta.stateID)
		if err != nil {
			return err
		}
		if state.Variants, err = mergeWeights(feature, current, m.weights); err != nil {
			return err
		}
	}

	var req api.UpdateFlagRequest
	if segmentID == 0 {
		req.EnvironmentDefault = &state
	} else {
		override := api.SegmentOverrideUpdate{
			Segment:  api.SegmentTarget{ID: segmentID},
			Enabled:  state.Enabled,
			Value:    state.Value,
			Variants: state.Variants,
		}
		if m.setPriority {
			override.Priority = &m.priority
		}
		// A new override starts from the environment default — enabled state and
		// position — stated outright rather than left to the server, so that a
		// value-only edit can't switch the segment off or jump the queue.
		if target == nil {
			if override.Enabled == nil {
				enabled := flagEnabled(feature.EnvironmentState)
				override.Enabled = &enabled
			}
			if override.Priority == nil {
				priority := feature.NumSegmentOverrides // joins at the end
				override.Priority = &priority
			}
		}
		req.SegmentOverrides = []api.SegmentOverrideUpdate{override}
	}

	errOut := cmd.ErrOrStderr()
	if ok, err := confirmed(cmd, fmt.Sprintf("Update %s %s?", name, scope), "changed"); !ok || err != nil {
		return err
	}

	resp, err := cred.client().UpdateFlag(cmd.Context(), env.APIKey, feature.ID, req)
	if err != nil {
		return err
	}

	if m.setValue {
		output.Success(errOut, "Set %s to %s %s", name, displayValue(*state.Value), scope)
	}
	if len(m.weights) > 0 {
		output.Success(errOut, "Set %s weights to %s %s", name, weightSummary(m.weights), scope)
	}
	if m.enable {
		output.Success(errOut, "Enabled %s %s", name, scope)
	}
	if m.disable {
		output.Success(errOut, "Disabled %s %s", name, scope)
	}
	if m.setPriority {
		output.Success(errOut, "Set %s priority to %d %s", name, m.priority, scope)
	}

	// Result model: an update also prints the resulting resource to stdout. The
	// response carries the flag's whole state in the environment, so the detail
	// renders from it rather than re-fetching the features list.
	updated := *feature
	if segmentID != 0 {
		override := resp.Override(segmentID)
		if override == nil {
			return bug.Mark(fmt.Errorf("the update of %s left no override for segment %s", name, meta.segment.display()))
		}
		scalar, err := scalarOfValue(override.Value)
		if err != nil {
			return err
		}
		updated.SegmentState = &api.FeatureState{Enabled: override.Enabled, Value: scalar}
		view := newSegmentFlagView(&updated, meta.segment, override.Priority)
		view.Variants = variantViews(feature, weightsOf(override.Variants))
		return renderSegmentDetail(cmd, view)
	}
	scalar, err := scalarOfValue(resp.EnvironmentDefault.Value)
	if err != nil {
		return err
	}
	updated.EnvironmentState = &api.FeatureState{Enabled: resp.EnvironmentDefault.Enabled, Value: scalar}
	return renderFlagDetail(cmd, &updated, weightsOf(resp.EnvironmentDefault.Variants))
}

// scalarOfValue converts a typed value from an update-flag response into the
// bare scalar the flag views render. A flag with no value at all reads as unset
// rather than as an empty string.
func scalarOfValue(v *api.FeatureValue) (any, error) {
	if v == nil {
		return nil, nil
	}
	return nativeScalar(*v)
}

var flagDeleteCmd = &cobra.Command{
	Use:   "delete <feature>",
	Short: "Delete a flag's segment override in the current environment",
	Example: `  # remove a segment or identity override (the flag itself is untouched)
  flagsmith flag delete onboarding --segment 12
  flagsmith flag delete onboarding --identifier user-123`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		hasSegment := cmd.Flags().Changed("segment")
		hasIdentifier := cmd.Flags().Changed("identifier")
		switch {
		case !hasSegment && !hasIdentifier:
			return usageErrorf("provide --segment <id> or --identifier <id> to delete an override")
		case hasSegment && hasIdentifier:
			return usageErrorf("--segment and --identifier are mutually exclusive")
		}
		_, cred, projectID, env, err := flagContext(cmd)
		if err != nil {
			return err
		}
		if hasIdentifier {
			return runIdentityDelete(cmd, cred, env, projectID, name, flagDeleteIdentifier)
		}
		feature, err := requireFeature(cmd, cred, projectID, env, 0, name)
		if err != nil {
			return err
		}
		segmentID, err := resolveSegmentID(cmd, cred, projectID, flagDeleteSegment)
		if err != nil {
			return err
		}
		return deleteSegmentOverride(cmd, cred, env, feature, segmentID)
	},
}

// deleteSegmentOverride removes one segment's override. The endpoint has no verb
// for deleting a single override: a PUT replaces the whole set, and whatever it
// leaves out is deleted. So the surviving overrides are read and echoed back in
// full — a partial echo would reset the state it omits. Being a
// read-modify-write, a change made to another override in between is lost.
func deleteSegmentOverride(cmd *cobra.Command, cred *activeCredential, env api.Environment, feature *api.Feature, segmentID int) error {
	fss, err := cred.client().FeatureSegments(cmd.Context(), env.ID, feature.ID)
	if err != nil {
		return err
	}
	survivors := make([]api.FeatureSegment, 0, len(fss))
	var target *api.FeatureSegment
	for i, fs := range fss {
		if fs.Segment == segmentID {
			target = &fss[i]
			continue
		}
		survivors = append(survivors, fs)
	}
	if target == nil {
		return withHint(
			fmt.Errorf("%s has no override for segment %s in %s",
				feature.Name, label(cachedSegmentName(segmentID), segmentID), environmentLabel(env)),
			fmt.Sprintf("Run `flagsmith flag list --feature %s` to see its segment overrides.", feature.Name))
	}
	segmentLabel := segmentRef{ID: segmentID, Name: target.SegmentName}.display()

	errOut := cmd.ErrOrStderr()
	prompt := fmt.Sprintf("delete %s override for segment %s in %s", feature.Name, segmentLabel, environmentLabel(env))
	if ok, err := confirmed(cmd, prompt+"?", "changed"); !ok || err != nil {
		return err
	}

	overrides, err := echoOverrides(cmd, cred, env, feature, survivors)
	if err != nil {
		return err
	}
	if _, err := cred.client().ReplaceFlag(cmd.Context(), env.APIKey, feature.ID,
		api.UpdateFlagRequest{SegmentOverrides: overrides}); err != nil {
		return err
	}
	output.Success(errOut, "Deleted %s override for segment %s in environment %s", feature.Name, segmentLabel, environmentLabel(env))
	return nil
}

// echoOverrides reads the current state of the given overrides and restates it
// in full, for a PUT that must preserve them. The result is never nil: an empty
// list is what deletes the last override, and must reach the wire as one.
func echoOverrides(cmd *cobra.Command, cred *activeCredential, env api.Environment, feature *api.Feature, keep []api.FeatureSegment) ([]api.SegmentOverrideUpdate, error) {
	overrides := make([]api.SegmentOverrideUpdate, 0, len(keep))
	if len(keep) == 0 {
		return overrides, nil
	}
	states, err := cred.client().FeatureStates(cmd.Context(), env.ID, feature.ID)
	if err != nil {
		return nil, err
	}
	byOverride := make(map[int]api.EnvironmentFeatureState, len(states))
	for _, s := range states {
		if s.FeatureSegment != nil {
			byOverride[*s.FeatureSegment] = s
		}
	}
	for _, fs := range keep {
		// Echoing a zero state would rewrite the override as off with no value,
		// so a missing one stops the write rather than guessing.
		state, ok := byOverride[fs.ID]
		if !ok {
			return nil, bug.Mark(fmt.Errorf("no feature state found for the override on segment %d; refusing to write", fs.Segment))
		}
		enabled, priority := state.Enabled, fs.Priority
		override := api.SegmentOverrideUpdate{
			Segment:  api.SegmentTarget{ID: fs.Segment},
			Enabled:  &enabled,
			Priority: &priority,
			Variants: echoVariants(feature, state.Multivariate),
		}
		// An override with no value of its own is restated by omission, since the
		// wire form has no way to say "no value": a replacing write inherits the
		// environment default for what it omits, which for a valueless flag is
		// the same nothing.
		if scalar := state.Value.Scalar(); scalar != nil {
			value := featureValueFromScalar(scalar)
			override.Value = &value
		}
		overrides = append(overrides, override)
	}
	return overrides, nil
}

// echoVariants restates a feature state's distribution as the full variant list
// the endpoint requires, so a replacing write leaves the weights alone. A
// feature without variants has none to send, and neither has a state with no
// allocations of its own — sending zeros would be a change, where omission
// restates the nothing that is already there.
func echoVariants(feature *api.Feature, allocations []api.MultivariateStateValue) []api.Variant {
	if len(feature.MultivariateOptions) == 0 || len(allocations) == 0 {
		return nil
	}
	weights := make(variantWeights, len(allocations))
	for _, a := range allocations {
		weights[a.OptionID] = a.Allocation
	}
	variants := make([]api.Variant, 0, len(feature.MultivariateOptions))
	for _, o := range feature.MultivariateOptions {
		variants = append(variants, api.Variant{ID: o.ID, Weight: weights[o.ID]})
	}
	return variants
}

var (
	flagToggleSegment    string
	flagToggleIdentifier string
)

var flagEnableCmd = &cobra.Command{
	Use:   "enable <feature>",
	Short: "Enable a flag (shorthand for `flag update --enable`)",
	Example: `  flagsmith flag enable onboarding

  # target a segment or identity override
  flagsmith flag enable onboarding --segment 12
  flagsmith flag enable onboarding --identifier user-123`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error { return runFlagToggle(cmd, args[0], true) },
}

var flagDisableCmd = &cobra.Command{
	Use:   "disable <feature>",
	Short: "Disable a flag (shorthand for `flag update --disable`)",
	Example: `  flagsmith flag disable onboarding
  flagsmith flag disable onboarding --segment 12`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error { return runFlagToggle(cmd, args[0], false) },
}

// runFlagToggle powers `flag enable` / `flag disable`: shorthands for
// `flag update --enable` / `--disable` that reuse the same update path.
func runFlagToggle(cmd *cobra.Command, name string, enable bool) error {
	if flagToggleSegment != "" && flagToggleIdentifier != "" {
		return usageErrorf("--segment and --identifier are mutually exclusive")
	}
	return applyFlagMutation(cmd, name, flagMutation{
		enable:     enable,
		disable:    !enable,
		segmentRef: flagToggleSegment,
		identifier: flagToggleIdentifier,
	})
}

// flagCreateCmd is hidden: it exists only to intercept `flag create` with a
// redirect, not to advertise a command (flags have no create — they exist per
// environment).
var flagCreateCmd = &cobra.Command{
	Use:    "create <feature>",
	Short:  "Not applicable — flags exist per environment (see `feature create`)",
	Hidden: true,
	Args:   cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		name := "<feature>"
		if len(args) > 0 {
			name = args[0]
		}
		return hintf(
			usageErrorf("flags exist per environment, so there is nothing to create"),
			"To create the feature itself, run `flagsmith feature create %s`.", name)
	},
}

// currentScalar returns a feature state's current value, or nil.
func currentScalar(fs *api.FeatureState) any {
	if fs == nil {
		return nil
	}
	return fs.Value
}

// featureValueFromScalar converts a bare scalar — read from the features list
// (JSON numbers arrive as float64) or from api.TypedValue.Scalar() (ints stay
// int) — into the {type, value} wire form update-flag expects.
func featureValueFromScalar(v any) api.FeatureValue {
	switch t := v.(type) {
	case bool:
		return api.FeatureValue{Type: "boolean", Value: strconv.FormatBool(t)}
	case int:
		return api.FeatureValue{Type: "integer", Value: strconv.Itoa(t)}
	case float64:
		if t == float64(int64(t)) {
			return api.FeatureValue{Type: "integer", Value: strconv.FormatInt(int64(t), 10)}
		}
		return api.FeatureValue{Type: "string", Value: strconv.FormatFloat(t, 'f', -1, 64)}
	case string:
		return api.FeatureValue{Type: "string", Value: t}
	default: // nil or an unexpected shape → empty string, Flagsmith's default
		return api.FeatureValue{Type: "string", Value: ""}
	}
}

// inferFeatureValue types a --value literal, honouring an explicit --type.
// Inference: true/false → boolean, all-digit → integer, otherwise string.
func inferFeatureValue(raw, typeFlag string) (api.FeatureValue, error) {
	switch typeFlag {
	case "":
		if raw == "true" || raw == "false" {
			return api.FeatureValue{Type: "boolean", Value: raw}, nil
		}
		if _, err := strconv.Atoi(raw); err == nil {
			return api.FeatureValue{Type: "integer", Value: raw}, nil
		}
		return api.FeatureValue{Type: "string", Value: raw}, nil
	case "string":
		return api.FeatureValue{Type: "string", Value: raw}, nil
	case "integer":
		if _, err := strconv.Atoi(raw); err != nil {
			return api.FeatureValue{}, usageErrorf("--value %q is not an integer", raw)
		}
		return api.FeatureValue{Type: "integer", Value: raw}, nil
	case "boolean":
		if raw != "true" && raw != "false" {
			return api.FeatureValue{}, usageErrorf("--value %q is not a boolean (use true or false)", raw)
		}
		return api.FeatureValue{Type: "boolean", Value: raw}, nil
	default:
		return api.FeatureValue{}, usageErrorf("invalid --type %q (want string, integer, or boolean)", typeFlag)
	}
}

// displayValue renders a value for a confirmation line: strings quoted, other
// types bare.
func displayValue(v api.FeatureValue) string {
	if v.Type == "string" {
		return strconv.Quote(v.Value)
	}
	return v.Value
}

func init() {
	flagUpdateCmd.Flags().BoolVar(&flagEnableFlag, "enable", false, "turn the flag on")
	flagUpdateCmd.Flags().BoolVar(&flagDisableFlag, "disable", false, "turn the flag off")
	flagUpdateCmd.Flags().StringVar(&flagValueFlag, "value", "", "set the flag value")
	flagUpdateCmd.Flags().StringVar(&flagTypeFlag, "type", "", "force the value type: string, integer, or boolean")
	flagUpdateCmd.Flags().StringVar(&flagUpdateSegment, "segment", "", "target this segment's override (id or name) instead of the environment default")
	flagUpdateCmd.Flags().StringVarP(&flagUpdateIdentifier, "identifier", "i", "", "target this identity's override instead of the environment default")
	flagUpdateCmd.Flags().IntVar(&flagUpdatePriority, "priority", 0, "move the segment override to this priority (0 is evaluated first)")
	flagDeleteCmd.Flags().StringVar(&flagDeleteSegment, "segment", "", "the segment (id or name) whose override to delete")
	flagDeleteCmd.Flags().StringVarP(&flagDeleteIdentifier, "identifier", "i", "", "the identity whose override to delete")
	flagUpdateCmd.Flags().StringArrayVar(&flagWeightFlags, "weight", nil,
		"set a variant's weight, as <key|id>=<percentage> (repeatable, or comma-separated)")
	for _, c := range []*cobra.Command{flagEnableCmd, flagDisableCmd} {
		c.Flags().StringVar(&flagToggleSegment, "segment", "", "target this segment's override (id or name) instead of the environment default")
		c.Flags().StringVarP(&flagToggleIdentifier, "identifier", "i", "", "target this identity's override instead of the environment default")
	}
}
