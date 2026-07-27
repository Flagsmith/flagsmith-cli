package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/cache"
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

func renderEnvironment(cmd *cobra.Command, e *api.Environment) error {
	return output.Render(cmd.OutOrStdout(), e, outputOpts(), func(w io.Writer) error {
		// The project name comes from the local name cache — strictly
		// cosmetic, so a miss degrades to the bare id rather than paying a
		// round-trip for a label (04 §3).
		projLabel := label(cache.Load(apiURL).Projects[strconv.Itoa(e.Project)], e.Project)
		return output.Detail(w, []output.Field{
			{Label: "Environment", Value: label(e.Name, e.APIKey)},
			{Label: "Project", Value: projLabel},
			{Label: "Description", Value: e.Description},
			{Label: "Versioning", Value: versioningLabel(e.UseV2FeatureVersioning)},
		})
	})
}

var environmentListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List environments in the current project",
	Example: "  flagsmith environment list",
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		envs, err := cred.client().Environments(cmd.Context(), projectID)
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
	Use:     "get <environment>",
	Short:   "Show an environment",
	Example: "  flagsmith environment get Production",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		ref, err := resolveEnvironmentRef(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		// The human detail's fields are all in the resolved list row. The
		// retrieve payload is richer (metadata), so machine output re-fetches
		// for fidelity.
		env := ref
		if jsonOutput() {
			if env, err = cred.client().GetEnvironment(cmd.Context(), ref.APIKey); err != nil {
				return err
			}
		}
		return renderEnvironment(cmd, env)
	},
}

var environmentCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create an environment (mints a client-side key)",
	Example: `  # the project comes from context
  flagsmith environment create Staging --description "Pre-prod"`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		body := envBodyFromFlags(cmd)
		body["name"] = args[0]
		body["project"] = projectID
		env, err := cred.client().CreateEnvironment(cmd.Context(), body)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Created environment %s", label(env.Name, env.APIKey))
		return renderEnvironment(cmd, env)
	},
}

var environmentUpdateCmd = &cobra.Command{
	Use:   "update <environment>",
	Short: "Update an environment",
	Example: `  flagsmith environment update Staging --description "Pre-prod"
  flagsmith environment update Production --hide-disabled-flags --banner-text "Live"`,
	Args: cobra.ExactArgs(1),
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
		env, err := cred.client().UpdateEnvironment(cmd.Context(), ref.APIKey, body)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Updated environment %s", label(env.Name, env.APIKey))
		return renderEnvironment(cmd, env)
	},
}

var environmentDeleteCmd = &cobra.Command{
	Use:     "delete <environment>",
	Short:   "Delete an environment",
	Example: "  flagsmith environment delete Staging --yes",
	Args:    cobra.ExactArgs(1),
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
		if ok, err := confirmed(cmd, fmt.Sprintf("delete environment %s", label(ref.Name, ref.APIKey)), "deleted"); !ok || err != nil {
			return err
		}
		if err := cred.client().DeleteEnvironment(cmd.Context(), ref.APIKey); err != nil {
			return err
		}
		output.Success(errOut, "Deleted environment %s", label(ref.Name, ref.APIKey))
		return nil
	},
}

var environmentCloneCmd = &cobra.Command{
	Use:     "clone <environment> <name>",
	Short:   "Clone an environment into a new one",
	Example: `  flagsmith environment clone Production "Production Copy"`,
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		ref, err := resolveEnvironmentRef(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		clone, err := cred.client().CloneEnvironment(cmd.Context(), ref.APIKey, map[string]any{"name": args[1]})
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Cloned %s into %s", ref.Name, label(clone.Name, clone.APIKey))
		return renderEnvironment(cmd, clone)
	},
}

var environmentDocumentCmd = &cobra.Command{
	Use:   "document [environment]",
	Short: "Output the environment document (JSON for local SDK evaluation)",
	Example: `  # the context environment, or a named one
  flagsmith environment document > env.json
  flagsmith environment document Production --jq '.feature_states | length'`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pc, err := applyContext(cmd)
		if err != nil {
			return err
		}
		cred, err := resolveCredential(cmd.Context())
		if err != nil {
			return err
		}
		projectID, err := resolveProjectID(cmd, pc, cred)
		if err != nil {
			return err
		}
		var apiKey string
		if len(args) == 1 {
			ref, err := resolveEnvironmentRef(cmd, cred, projectID, args[0])
			if err != nil {
				return err
			}
			apiKey = ref.APIKey
		} else {
			env, err := resolveEnvironment(cmd, pc, cred, projectID)
			if err != nil {
				return err
			}
			apiKey = env.APIKey
		}
		doc, err := cred.client().EnvironmentDocument(cmd.Context(), apiKey)
		if err != nil {
			return err
		}
		var parsed any
		if err := json.Unmarshal(doc, &parsed); err != nil {
			return err
		}
		// A document is always JSON; --jq composes.
		return output.Render(cmd.OutOrStdout(), parsed, output.Options{JSON: true, JQ: jqFlag}, nil)
	},
}

var environmentKeyCmd = &cobra.Command{
	Use:   "key",
	Short: "Manage an environment's server-side SDK keys",
}

var envKeyNameFlag string

func valueOrDash(s *string) string {
	if s == nil || *s == "" {
		return "-"
	}
	return *s
}

var environmentKeyListCmd = &cobra.Command{
	Use:     "list <environment>",
	Short:   "List an environment's server-side keys",
	Example: "  flagsmith environment key list Production",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		ref, err := resolveEnvironmentRef(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		keys, err := cred.client().EnvironmentAPIKeys(cmd.Context(), ref.APIKey)
		if err != nil {
			return err
		}
		return output.Render(cmd.OutOrStdout(), keys, outputOpts(), func(w io.Writer) error {
			if len(keys) == 0 {
				fmt.Fprintln(w, "No server-side keys.")
				return nil
			}
			rows := make([][]string, len(keys))
			for i, k := range keys {
				rows[i] = []string{k.Name, strconv.Itoa(k.ID), strconv.FormatBool(k.Active), k.CreatedAt, valueOrDash(k.ExpiresAt)}
			}
			return output.Table(w, []string{"NAME", "ID", "ACTIVE", "CREATED", "EXPIRES AT"}, rows)
		})
	},
}

var environmentKeyCreateCmd = &cobra.Command{
	Use:     "create <environment>",
	Short:   "Create a server-side key (its value is shown once)",
	Example: `  flagsmith environment key create Production --name backend`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		ref, err := resolveEnvironmentRef(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		body := map[string]any{}
		if cmd.Flags().Changed("name") {
			body["name"] = envKeyNameFlag
		}
		key, err := cred.client().CreateEnvironmentAPIKey(cmd.Context(), ref.APIKey, body)
		if err != nil {
			return err
		}
		errOut := cmd.ErrOrStderr()
		output.Success(errOut, "Created server-side key %s", label(key.Name, key.ID))
		return output.Render(cmd.OutOrStdout(), key, outputOpts(), func(w io.Writer) error {
			fmt.Fprintln(errOut, "Store this now — it will not be shown again:")
			fmt.Fprintln(w, key.Key)
			return nil
		})
	},
}

var environmentKeyDeleteCmd = &cobra.Command{
	Use:     "delete <environment> <key-id>",
	Short:   "Delete a server-side key",
	Example: "  flagsmith environment key delete Production 14 --yes",
	Args:    cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		keyID, err := strconv.Atoi(args[1])
		if err != nil {
			return usageErrorf("key id must be a number, got %q", args[1])
		}
		cred, projectID, err := projectScopedContext(cmd)
		if err != nil {
			return err
		}
		ref, err := resolveEnvironmentRef(cmd, cred, projectID, args[0])
		if err != nil {
			return err
		}
		errOut := cmd.ErrOrStderr()
		if ok, err := confirmed(cmd, fmt.Sprintf("delete server-side key %d from %s", keyID, ref.Name), "deleted"); !ok || err != nil {
			return err
		}
		if err := cred.client().DeleteEnvironmentAPIKey(cmd.Context(), ref.APIKey, keyID); err != nil {
			return err
		}
		output.Success(errOut, "Deleted server-side key %d from %s", keyID, ref.Name)
		return nil
	},
}

func init() {
	environmentKeyCreateCmd.Flags().StringVar(&envKeyNameFlag, "name", "", "a name for the key")
	environmentKeyCmd.AddCommand(environmentKeyListCmd, environmentKeyCreateCmd, environmentKeyDeleteCmd)
	environmentCmd.AddCommand(environmentKeyCmd)
	for _, c := range []*cobra.Command{environmentCreateCmd, environmentUpdateCmd} {
		c.Flags().StringVar(&envDescriptionFlag, "description", "", "environment description")
		c.Flags().BoolVar(&envHideDisabledFlag, "hide-disabled-flags", false, "hide disabled flags from the SDK")
		c.Flags().BoolVar(&envAllowTraitsFlag, "allow-client-traits", false, "allow client SDKs to set traits")
		c.Flags().StringVar(&envBannerTextFlag, "banner-text", "", "dashboard banner text")
	}
	environmentUpdateCmd.Flags().StringVar(&envNameFlag, "name", "", "rename the environment")
	environmentCmd.AddCommand(environmentListCmd, environmentGetCmd, environmentCreateCmd, environmentUpdateCmd, environmentDeleteCmd, environmentCloneCmd, environmentDocumentCmd)
	rootCmd.AddCommand(environmentCmd)
}
