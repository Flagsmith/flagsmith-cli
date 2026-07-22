package cmd

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

var environmentCmd = &cobra.Command{
	Use:     "environment",
	Aliases: []string{"env"},
	Short:   "Manage environments in the current project",
}

var (
	envNameFlag         string
	envDescriptionFlag  string
	envHideDisabledFlag bool
	envAllowTraitsFlag  bool
	envBannerTextFlag   string
)

// resolveEnvironmentRef turns an environment reference (name or api_key) into
// its full record, scoped to the project.
func resolveEnvironmentRef(cmd *cobra.Command, cred *activeCredential, projectID int, ref string) (*api.Environment, error) {
	envs, err := api.Environments(cmd.Context(), apiURL, cred.auth, projectID)
	if err != nil {
		return nil, err
	}
	for i := range envs {
		if envs[i].APIKey == ref {
			return &envs[i], nil
		}
	}
	byKey := make(map[string]string, len(envs))
	for _, e := range envs {
		byKey[e.APIKey] = e.Name
	}
	hits := matchByName(byKey, ref)
	if len(hits) == 0 {
		return nil, fmt.Errorf("environment %q not found in project %d", ref, projectID)
	}
	chosen, err := pickCandidate(cmd, "environment", ref, hits, byKey)
	if err != nil {
		return nil, err
	}
	for i := range envs {
		if envs[i].APIKey == chosen {
			return &envs[i], nil
		}
	}
	return nil, fmt.Errorf("environment %q not found in project %d", ref, projectID)
}

func envBodyFromFlags(cmd *cobra.Command) map[string]any {
	body := map[string]any{}
	if cmd.Flags().Changed("name") {
		body["name"] = envNameFlag
	}
	if cmd.Flags().Changed("description") {
		body["description"] = envDescriptionFlag
	}
	if cmd.Flags().Changed("hide-disabled-flags") {
		body["hide_disabled_flags"] = envHideDisabledFlag
	}
	if cmd.Flags().Changed("allow-client-traits") {
		body["allow_client_traits"] = envAllowTraitsFlag
	}
	if cmd.Flags().Changed("banner-text") {
		body["banner_text"] = envBannerTextFlag
	}
	return body
}

func versioningLabel(v2 bool) string {
	if v2 {
		return "v2"
	}
	return "v1"
}

func renderEnvironment(cmd *cobra.Command, cred *activeCredential, e *api.Environment) error {
	return output.Render(cmd.OutOrStdout(), e, outputOpts(), func(w io.Writer) error {
		projLabel := strconv.Itoa(e.Project)
		if p, err := api.GetProject(cmd.Context(), apiURL, cred.auth, e.Project); err == nil && p.Name != "" {
			projLabel = fmt.Sprintf("%s (%d)", p.Name, e.Project)
		}
		return output.Detail(w, []output.Field{
			{Label: "Environment", Value: fmt.Sprintf("%s (%s)", e.Name, e.APIKey)},
			{Label: "Project", Value: projLabel},
			{Label: "Description", Value: e.Description},
			{Label: "Versioning", Value: versioningLabel(e.UseV2FeatureVersioning)},
		})
	})
}

var environmentListCmd = &cobra.Command{
	Use:   "list",
	Short: "List environments in the current project",
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		envs, err := api.Environments(cmd.Context(), apiURL, cred.auth, projectID)
		if err != nil {
			return err
		}
		return output.Render(cmd.OutOrStdout(), envs, outputOpts(), func(w io.Writer) error {
			if len(envs) == 0 {
				fmt.Fprintln(w, "No environments.")
				return nil
			}
			rows := make([][]string, len(envs))
			for i, e := range envs {
				rows[i] = []string{e.Name, e.APIKey, e.Description}
			}
			if err := output.Table(w, []string{"NAME", "KEY", "DESCRIPTION"}, rows); err != nil {
				return err
			}
			fmt.Fprintf(w, "\n%d %s\n", len(envs), plural(len(envs), "environment", "environments"))
			return nil
		})
	},
}

var environmentGetCmd = &cobra.Command{
	Use:   "get <environment>",
	Short: "Show an environment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		ref, err := resolveEnvironmentRef(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		env, err := api.GetEnvironment(cmd.Context(), apiURL, cred.auth, ref.APIKey)
		if err != nil {
			return err
		}
		return renderEnvironment(cmd, cred, env)
	},
}

var environmentCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create an environment (mints a client-side key)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		body := envBodyFromFlags(cmd)
		body["name"] = args[0]
		body["project"] = projectID
		env, err := api.CreateEnvironment(cmd.Context(), apiURL, cred.auth, body)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Created environment %s (%s)", env.Name, env.APIKey)
		return renderEnvironment(cmd, cred, env)
	},
}

var environmentUpdateCmd = &cobra.Command{
	Use:   "update <environment>",
	Short: "Update an environment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body := envBodyFromFlags(cmd)
		if len(body) == 0 {
			return usageErrorf("nothing to update — pass --name, --description, or a setting flag")
		}
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		ref, err := resolveEnvironmentRef(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		env, err := api.UpdateEnvironment(cmd.Context(), apiURL, cred.auth, ref.APIKey, body)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Updated environment %s (%s)", env.Name, env.APIKey)
		return renderEnvironment(cmd, cred, env)
	},
}

var environmentDeleteCmd = &cobra.Command{
	Use:   "delete <environment>",
	Short: "Delete an environment",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		ref, err := resolveEnvironmentRef(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		errOut := cmd.ErrOrStderr()
		if ok, err := confirmOrYes(cmd, fmt.Sprintf("delete environment %s (%s)", ref.Name, ref.APIKey)); err != nil {
			return err
		} else if !ok {
			fmt.Fprintln(errOut, "Aborted; nothing deleted.")
			return nil
		}
		if err := api.DeleteEnvironment(cmd.Context(), apiURL, cred.auth, ref.APIKey); err != nil {
			return err
		}
		output.Success(errOut, "Deleted environment %s (%s)", ref.Name, ref.APIKey)
		return nil
	},
}

var environmentCloneCmd = &cobra.Command{
	Use:   "clone <environment> <name>",
	Short: "Clone an environment into a new one",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		ref, err := resolveEnvironmentRef(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		clone, err := api.CloneEnvironment(cmd.Context(), apiURL, cred.auth, ref.APIKey, map[string]any{"name": args[1]})
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Cloned %s into %s (%s)", ref.Name, clone.Name, clone.APIKey)
		return renderEnvironment(cmd, cred, clone)
	},
}

func init() {
	for _, c := range []*cobra.Command{environmentCreateCmd, environmentUpdateCmd} {
		c.Flags().StringVar(&envDescriptionFlag, "description", "", "environment description")
		c.Flags().BoolVar(&envHideDisabledFlag, "hide-disabled-flags", false, "hide disabled flags from the SDK")
		c.Flags().BoolVar(&envAllowTraitsFlag, "allow-client-traits", false, "allow client SDKs to set traits")
		c.Flags().StringVar(&envBannerTextFlag, "banner-text", "", "dashboard banner text")
	}
	environmentUpdateCmd.Flags().StringVar(&envNameFlag, "name", "", "rename the environment")
	environmentCmd.AddCommand(environmentListCmd, environmentGetCmd, environmentCreateCmd, environmentUpdateCmd, environmentDeleteCmd, environmentCloneCmd)
	rootCmd.AddCommand(environmentCmd)
}
