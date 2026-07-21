package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

var (
	flagEnableFlag    bool
	flagDisableFlag   bool
	flagValueFlag     string
	flagTypeFlag      string
	flagUpdateSegment int
	flagDeleteSegment int
)

var flagUpdateCmd = &cobra.Command{
	Use:   "update <feature>",
	Short: "Change a flag's state in the current environment",
	Args:  cobra.ExactArgs(1),
	RunE:  runFlagUpdate,
}

func runFlagUpdate(cmd *cobra.Command, args []string) error {
	name := args[0]
	enable := cmd.Flags().Changed("enable")
	disable := cmd.Flags().Changed("disable")
	setValue := cmd.Flags().Changed("value")

	switch {
	case enable && disable:
		return usageErrorf("--enable and --disable are mutually exclusive")
	case !enable && !disable && !setValue:
		return usageErrorf("nothing to update — pass --enable, --disable, or --value")
	case cmd.Flags().Changed("type") && !setValue:
		return usageErrorf("--type only applies together with --value")
	}

	segmentID := flagUpdateSegment
	_, cred, projectID, env, err := flagContext(cmd)
	if err != nil {
		return err
	}
	features, err := api.Features(cmd.Context(), apiURL, cred.auth, projectID, env.ID, segmentID)
	if err != nil {
		return err
	}
	feature := findFeature(features, name)
	if feature == nil {
		return fmt.Errorf("feature %q not found in %s", name, environmentLabel(env))
	}

	// update-flag-v2 always requires the whole environment default, so carry it
	// forward unchanged.
	req := api.UpdateFlagRequest{
		Feature: api.FeatureRef{Name: name},
		EnvironmentDefault: api.EnvironmentDefault{
			Enabled: flagEnabled(feature.EnvironmentState),
			Value:   featureValueFromScalar(currentScalar(feature.EnvironmentState)),
		},
	}

	// The state being changed: the environment default, or a segment override.
	target := feature.EnvironmentState
	scope := "environment " + environmentLabel(env)
	if segmentID != 0 {
		target = feature.SegmentState // nil when the override does not exist yet
		scope = fmt.Sprintf("segment %d in environment %s", segmentID, environmentLabel(env))
	}

	enabled := flagEnabled(target)
	if enable {
		enabled = true
	}
	if disable {
		enabled = false
	}
	// A new segment override with no explicit value inherits the environment
	// default; otherwise keep the target's current value.
	value := req.EnvironmentDefault.Value
	if target != nil {
		value = featureValueFromScalar(currentScalar(target))
	}
	if setValue {
		if value, err = inferFeatureValue(flagValueFlag, flagTypeFlag); err != nil {
			return err
		}
	}

	if segmentID == 0 {
		req.EnvironmentDefault.Enabled = enabled
		req.EnvironmentDefault.Value = value
	} else {
		req.SegmentOverrides = []api.SegmentOverride{{SegmentID: segmentID, Enabled: enabled, Value: value}}
	}

	errOut := cmd.ErrOrStderr()
	if ok, err := confirmOrYes(cmd, fmt.Sprintf("Update %s in %s?", name, scope)); err != nil {
		return err
	} else if !ok {
		fmt.Fprintln(errOut, "Aborted; nothing changed.")
		return nil
	}

	if err := api.UpdateFlag(cmd.Context(), apiURL, cred.auth, env.APIKey, req); err != nil {
		return err
	}

	if setValue {
		output.Success(errOut, "Set %s to %s in %s", name, displayValue(value), scope)
	}
	if enable {
		output.Success(errOut, "Enabled %s in %s", name, scope)
	}
	if disable {
		output.Success(errOut, "Disabled %s in %s", name, scope)
	}

	// Result model: an update also prints the resulting resource to stdout.
	features, err = api.Features(cmd.Context(), apiURL, cred.auth, projectID, env.ID, segmentID)
	if err != nil {
		return err
	}
	updated := findFeature(features, name)
	if updated == nil {
		return nil
	}
	if segmentID != 0 {
		return renderSegmentDetail(cmd, updated, segmentID)
	}
	return renderFlagDetail(cmd, updated)
}

var flagDeleteCmd = &cobra.Command{
	Use:   "delete <feature>",
	Short: "Delete a flag's segment override in the current environment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		if !cmd.Flags().Changed("segment") {
			return usageErrorf("provide --segment <id> to delete a segment override")
		}
		_, cred, _, env, err := flagContext(cmd)
		if err != nil {
			return err
		}
		errOut := cmd.ErrOrStderr()
		label := fmt.Sprintf("delete %s override for segment %d in %s", name, flagDeleteSegment, environmentLabel(env))
		if ok, err := confirmOrYes(cmd, label+"?"); err != nil {
			return err
		} else if !ok {
			fmt.Fprintln(errOut, "Aborted; nothing changed.")
			return nil
		}
		if err := api.DeleteSegmentOverride(cmd.Context(), apiURL, cred.auth, env.APIKey, name, flagDeleteSegment); err != nil {
			return err
		}
		output.Success(errOut, "Deleted %s override for segment %d in environment %s", name, flagDeleteSegment, environmentLabel(env))
		return nil
	},
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
		return usageErrorf(
			"flags exist per environment, so there is nothing to create.\nTo create the feature itself, use `flagsmith feature create %s`.", name)
	},
}

// currentScalar returns a feature state's current value, or nil.
func currentScalar(fs *api.FeatureState) any {
	if fs == nil {
		return nil
	}
	return fs.Value
}

// featureValueFromScalar converts a value read from the features list (a bare
// JSON scalar) into the {type, value} wire form update-flag-v2 expects.
func featureValueFromScalar(v any) api.FeatureValue {
	switch t := v.(type) {
	case bool:
		return api.FeatureValue{Type: "boolean", Value: strconv.FormatBool(t)}
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
	flagUpdateCmd.Flags().IntVar(&flagUpdateSegment, "segment", 0, "target this segment's override instead of the environment default")
	flagDeleteCmd.Flags().IntVar(&flagDeleteSegment, "segment", 0, "the segment id whose override to delete")
}
