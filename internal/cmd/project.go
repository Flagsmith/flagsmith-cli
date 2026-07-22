package cmd

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

var projectCmd = &cobra.Command{
	Use:   "project",
	Short: "Manage projects",
}

var (
	projectNameFlag         string
	projectHideDisabledFlag bool
	projectFeatureRegexFlag string
)

// resolveProjectRefID turns a project reference (id or name) into an id.
func resolveProjectRefID(cmd *cobra.Command, cred *activeCredential, ref string) (int, error) {
	if id, err := strconv.Atoi(ref); err == nil {
		return id, nil
	}
	projects, err := api.Projects(cmd.Context(), apiURL, cred.auth, 0)
	if err != nil {
		return 0, err
	}
	byID := make(map[string]string, len(projects))
	for _, p := range projects {
		byID[strconv.Itoa(p.ID)] = p.Name
	}
	hits := matchByName(byID, ref)
	if len(hits) == 0 {
		return 0, fmt.Errorf("project %q not found", ref)
	}
	chosen, err := pickCandidate(cmd, "project", ref, hits, byID)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(chosen)
}

// orgNameMap maps organisation id → name for display (best effort).
func orgNameMap(cmd *cobra.Command, cred *activeCredential) map[int]string {
	m := map[int]string{}
	orgs, err := api.Organisations(cmd.Context(), apiURL, cred.auth)
	if err != nil {
		return m
	}
	for _, o := range orgs {
		m[o.ID] = o.Name
	}
	return m
}

func orgLabel(names map[int]string, id int) string {
	if name := names[id]; name != "" {
		return fmt.Sprintf("%s (%d)", name, id)
	}
	return strconv.Itoa(id)
}

func projectBodyFromFlags(cmd *cobra.Command) map[string]any {
	body := map[string]any{}
	if cmd.Flags().Changed("name") {
		body["name"] = projectNameFlag
	}
	if cmd.Flags().Changed("hide-disabled-flags") {
		body["hide_disabled_flags"] = projectHideDisabledFlag
	}
	if cmd.Flags().Changed("feature-name-regex") {
		body["feature_name_regex"] = projectFeatureRegexFlag
	}
	return body
}

func renderProject(cmd *cobra.Command, cred *activeCredential, p *api.Project) error {
	return output.Render(cmd.OutOrStdout(), p, outputOpts(), func(w io.Writer) error {
		return output.Detail(w, []output.Field{
			{Label: "Project", Value: fmt.Sprintf("%s (%d)", p.Name, p.ID)},
			{Label: "Organisation", Value: orgLabel(orgNameMap(cmd, cred), p.Organisation)},
		})
	})
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List projects",
	RunE: func(cmd *cobra.Command, args []string) error {
		pc, err := applyContext(cmd)
		if err != nil {
			return err
		}
		cred, err := resolveCredential(cmd.Context())
		if err != nil {
			return err
		}
		orgID := 0
		if cmd.Flags().Changed("organisation") {
			if orgID, err = resolveOrganisationID(cmd, pc, cred); err != nil {
				return err
			}
		}
		projects, err := api.Projects(cmd.Context(), apiURL, cred.auth, orgID)
		if err != nil {
			return err
		}
		return output.Render(cmd.OutOrStdout(), projects, outputOpts(), func(w io.Writer) error {
			if len(projects) == 0 {
				fmt.Fprintln(w, "No projects.")
				return nil
			}
			names := orgNameMap(cmd, cred)
			rows := make([][]string, len(projects))
			for i, p := range projects {
				rows[i] = []string{p.Name, strconv.Itoa(p.ID), orgLabel(names, p.Organisation)}
			}
			if err := output.Table(w, []string{"NAME", "ID", "ORGANISATION"}, rows); err != nil {
				return err
			}
			fmt.Fprintf(w, "\n%d %s\n", len(projects), plural(len(projects), "project", "projects"))
			return nil
		})
	},
}

var projectGetCmd = &cobra.Command{
	Use:   "get <project>",
	Short: "Show a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, err := credentialContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveProjectRefID(cmd, cred, args[0])
		if err != nil {
			return err
		}
		p, err := api.GetProject(cmd.Context(), apiURL, cred.auth, id)
		if err != nil {
			return err
		}
		return renderProject(cmd, cred, p)
	},
}

var projectCreateCmd = &cobra.Command{
	Use:   "create <name>",
	Short: "Create a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pc, err := applyContext(cmd)
		if err != nil {
			return err
		}
		cred, err := resolveCredential(cmd.Context())
		if err != nil {
			return err
		}
		orgID, err := resolveOrganisationID(cmd, pc, cred)
		if err != nil {
			return err
		}
		body := projectBodyFromFlags(cmd)
		body["name"] = args[0]
		body["organisation"] = orgID
		p, err := api.CreateProject(cmd.Context(), apiURL, cred.auth, body)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Created project %s (%d)", p.Name, p.ID)
		return renderProject(cmd, cred, p)
	},
}

var projectUpdateCmd = &cobra.Command{
	Use:   "update <project>",
	Short: "Update a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body := projectBodyFromFlags(cmd)
		if len(body) == 0 {
			return usageErrorf("nothing to update — pass --name, --hide-disabled-flags, or --feature-name-regex")
		}
		cred, err := credentialContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveProjectRefID(cmd, cred, args[0])
		if err != nil {
			return err
		}
		p, err := api.UpdateProject(cmd.Context(), apiURL, cred.auth, id, body)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Updated project %s (%d)", p.Name, p.ID)
		return renderProject(cmd, cred, p)
	},
}

var projectDeleteCmd = &cobra.Command{
	Use:   "delete <project>",
	Short: "Delete a project",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, err := credentialContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveProjectRefID(cmd, cred, args[0])
		if err != nil {
			return err
		}
		errOut := cmd.ErrOrStderr()
		if ok, err := confirmOrYes(cmd, fmt.Sprintf("delete project %s (%d)", args[0], id)); err != nil {
			return err
		} else if !ok {
			fmt.Fprintln(errOut, "Aborted; nothing deleted.")
			return nil
		}
		if err := api.DeleteProject(cmd.Context(), apiURL, cred.auth, id); err != nil {
			return err
		}
		output.Success(errOut, "Deleted project %s (%d)", args[0], id)
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{projectCreateCmd, projectUpdateCmd} {
		c.Flags().BoolVar(&projectHideDisabledFlag, "hide-disabled-flags", false, "hide disabled flags from the SDK")
		c.Flags().StringVar(&projectFeatureRegexFlag, "feature-name-regex", "", "regex feature names must match")
	}
	projectUpdateCmd.Flags().StringVar(&projectNameFlag, "name", "", "rename the project")
	projectCmd.AddCommand(projectListCmd, projectGetCmd, projectCreateCmd, projectUpdateCmd, projectDeleteCmd)
	rootCmd.AddCommand(projectCmd)
}
