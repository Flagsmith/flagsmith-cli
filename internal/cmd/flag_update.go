package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/api"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/cache"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/output"
)

var (
	flagEnableFlag       bool
	flagDisableFlag      bool
	flagValueFlag        string
	flagTypeFlag         string
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
  flagsmith flag update onboarding --value beta --identifier user-123`,
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
	case !m.enable && !m.disable && !m.setValue && !m.setPriority:
		return usageErrorf("nothing to update — pass --enable, --disable, --value, or --priority")
	case cmd.Flags().Changed("type") && !m.setValue:
		return usageErrorf("--type only applies together with --value")
	}
	return applyFlagMutation(cmd, args[0], m)
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
	segmentRef                string
	identifier                string
}

// applyFlagMutation resolves the feature and applies m via update-flag-v2 (which
// requires the whole environment default, so the rest is carried forward
// unchanged), then reprints the resulting flag.
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
	name = feature.Name // canonical for the wire ref and messages

	if m.identifier != "" {
		return runIdentityUpdate(cmd, cred, env, projectID, feature, m.identifier, m.enable, m.disable, m.setValue)
	}

	req := api.UpdateFlagRequest{
		Feature: api.FeatureRef{Name: name},
		EnvironmentDefault: api.EnvironmentDefault{
			Enabled: flagEnabled(feature.EnvironmentState),
			Value:   featureValueFromScalar(currentScalar(feature.EnvironmentState)),
		},
	}

	// The state being changed: the environment default, or a segment override.
	// The scope carries its own preposition: "in environment …" for the
	// default, "for segment name (id) in environment …" for an override.
	// The override's metadata (name, current priority) is fetched once and
	// reused for the post-update render.
	target := feature.EnvironmentState
	scope := "in environment " + environmentLabel(env)
	var segment segmentRef
	var priority int
	if segmentID != 0 {
		target = feature.SegmentState // nil when the override does not exist yet
		var err error
		segment, priority, err = segmentOverrideMeta(cmd, cred, env.ID, feature.ID, segmentID)
		if err != nil {
			return err
		}
		scope = fmt.Sprintf("for segment %s in environment %s", segment.display(), environmentLabel(env))
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

	// A new segment override inherits the environment default — enabled state
	// and value alike; an existing one keeps its current state. A value-only
	// edit must never silently switch the segment off.
	enabled := flagEnabled(target)
	if target == nil {
		enabled = flagEnabled(feature.EnvironmentState)
	}
	if m.enable {
		enabled = true
	}
	if m.disable {
		enabled = false
	}
	value := req.EnvironmentDefault.Value
	if target != nil {
		value = featureValueFromScalar(currentScalar(target))
	}
	if m.setValue {
		if value, err = inferFeatureValue(flagValueFlag, flagTypeFlag); err != nil {
			return err
		}
	}

	if segmentID == 0 {
		req.EnvironmentDefault.Enabled = enabled
		req.EnvironmentDefault.Value = value
	} else {
		override := api.SegmentOverride{SegmentID: segmentID, Enabled: enabled, Value: value}
		if m.setPriority {
			override.Priority = &m.priority
		}
		req.SegmentOverrides = []api.SegmentOverride{override}
	}

	errOut := cmd.ErrOrStderr()
	if ok, err := confirmed(cmd, fmt.Sprintf("Update %s %s?", name, scope), "changed"); !ok || err != nil {
		return err
	}

	if err := cred.client().UpdateFlag(cmd.Context(), env.APIKey, req); err != nil {
		return err
	}

	if m.setValue {
		output.Success(errOut, "Set %s to %s %s", name, displayValue(value), scope)
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
	// request carried the written state in full, so the detail renders from it
	// rather than re-fetching the features list.
	scalar, err := nativeScalar(value)
	if err != nil {
		return err
	}
	updated := *feature
	if segmentID != 0 {
		updated.SegmentState = &api.FeatureState{Enabled: enabled, Value: scalar}
		if target == nil {
			priority = feature.NumSegmentOverrides // a new override joins at the end
		}
		if m.setPriority {
			priority = m.priority
		}
		return renderSegmentDetail(cmd, newSegmentFlagView(&updated, segment, priority))
	}
	updated.EnvironmentState = &api.FeatureState{Enabled: enabled, Value: scalar}
	return renderFlagDetail(cmd, &updated)
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
		segmentID, err := resolveSegmentID(cmd, cred, projectID, flagDeleteSegment)
		if err != nil {
			return err
		}
		// Nothing on this path carries the segment's name, so the display comes
		// from the name cache (seeded by resolving a name ref) and degrades to
		// the bare id.
		segmentLabel := label(cache.Load(apiURL).Segments[strconv.Itoa(segmentID)], segmentID)
		errOut := cmd.ErrOrStderr()
		prompt := fmt.Sprintf("delete %s override for segment %s in %s", name, segmentLabel, environmentLabel(env))
		if ok, err := confirmed(cmd, prompt+"?", "changed"); !ok || err != nil {
			return err
		}
		if err := cred.client().DeleteSegmentOverride(cmd.Context(), env.APIKey, featureRefFor(name), segmentID); err != nil {
			return err
		}
		output.Success(errOut, "Deleted %s override for segment %s in environment %s", name, segmentLabel, environmentLabel(env))
		return nil
	},
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

// featureRefFor parses a feature reference into the update-flag wire form:
// all-digit → id, anything else → name, resolved server-side.
func featureRefFor(ref string) api.FeatureRef {
	if id, err := strconv.Atoi(ref); err == nil {
		return api.FeatureRef{ID: id}
	}
	return api.FeatureRef{Name: ref}
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
// int) — into the {type, value} wire form update-flag-v2 expects.
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
	for _, c := range []*cobra.Command{flagEnableCmd, flagDisableCmd} {
		c.Flags().StringVar(&flagToggleSegment, "segment", "", "target this segment's override (id or name) instead of the environment default")
		c.Flags().StringVarP(&flagToggleIdentifier, "identifier", "i", "", "target this identity's override instead of the environment default")
	}
}
