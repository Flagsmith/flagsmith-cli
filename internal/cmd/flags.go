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

func flagValue(fs *api.FeatureState) string {
	if fs == nil || fs.Value == nil {
		return "-"
	}
	return fmt.Sprint(fs.Value)
}

func flagEnabled(fs *api.FeatureState) bool {
	return fs != nil && fs.Enabled
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

var flagListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every flag in the current environment",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cred, projectID, env, err := flagContext(cmd)
		if err != nil {
			return err
		}
		features, err := api.Features(cmd.Context(), apiURL, cred.auth, projectID, env.ID)
		if err != nil {
			return err
		}
		return output.Render(cmd.OutOrStdout(), features, outputOpts(), func(w io.Writer) error {
			if len(features) == 0 {
				fmt.Fprintln(w, "No flags.")
				return nil
			}
			rows := make([][]string, len(features))
			for i, f := range features {
				rows[i] = []string{
					f.Name,
					f.Type,
					strconv.FormatBool(flagEnabled(f.EnvironmentState)),
					flagValue(f.EnvironmentState),
					lifecycleOrDash(f.LifecycleStage),
				}
			}
			if err := output.Table(w, []string{"NAME", "TYPE", "ENABLED", "VALUE", "LIFECYCLE"}, rows); err != nil {
				return err
			}
			fmt.Fprintf(w, "\n%d %s\n", len(features), plural(len(features), "flag", "flags"))
			return nil
		})
	},
}

var flagGetCmd = &cobra.Command{
	Use:   "get <feature>",
	Short: "Show a single flag's state in the current environment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		_, cred, projectID, env, err := flagContext(cmd)
		if err != nil {
			return err
		}
		// search is a case-insensitive contains match, so narrow to the exact
		// feature client-side.
		features, err := api.Features(cmd.Context(), apiURL, cred.auth, projectID, env.ID)
		if err != nil {
			return err
		}
		var feature *api.Feature
		for i := range features {
			if strings.EqualFold(features[i].Name, name) {
				feature = &features[i]
				break
			}
		}
		if feature == nil {
			return fmt.Errorf("feature %q not found in %s", name, environmentLabel(env))
		}
		return output.Render(cmd.OutOrStdout(), feature, outputOpts(), func(w io.Writer) error {
			fields := []output.Field{
				{Label: "Feature", Value: feature.Name},
				{Label: "Description", Value: feature.Description},
				{Label: "Type", Value: feature.Type},
				{Label: "Enabled", Value: strconv.FormatBool(flagEnabled(feature.EnvironmentState))},
				{Label: "Value", Value: flagValue(feature.EnvironmentState)},
				{Label: "Segment overrides", Value: strconv.Itoa(feature.NumSegmentOverrides)},
				{Label: "Identity overrides", Value: identityOverrides(feature.NumIdentityOverrides)},
				{Label: "Code references", Value: strconv.Itoa(feature.CodeReferences())},
				{Label: "Lifecycle stage", Value: lifecycleOrDash(feature.LifecycleStage)},
			}
			return output.Detail(w, fields)
		})
	},
}

func lifecycleOrDash(stage string) string {
	if stage == "" {
		return "-"
	}
	return stage
}

func identityOverrides(n *int) string {
	if n == nil {
		return "-" // Edge/Dynamo projects do not report this
	}
	return strconv.Itoa(*n)
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
	flagCmd.AddCommand(flagListCmd, flagGetCmd)
	rootCmd.AddCommand(flagCmd)
}
