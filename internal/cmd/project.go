package cmd

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/cache"
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
	projects, err := cred.client().Projects(cmd.Context(), 0)
	if err != nil {
		return 0, err
	}
	byID := make(map[string]string, len(projects))
	for _, p := range projects {
		byID[strconv.Itoa(p.ID)] = p.Name
	}
	hits := matchByName(byID, ref)
	if len(hits) == 0 {
		return 0, withHint(
			fmt.Errorf("project %q not found", ref),
			hintProjectList)
	}
	chosen, err := pickCandidate(cmd, "project", "id", ref, hits, byID)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(chosen)
}

// orgLabels maps the given organisation ids to names for display, from the
// local name cache when it covers them all. Only a miss pays one
// Organisations fetch, which reseeds the cache (04 §3); labels stay
// best-effort either way.
func orgLabels(cmd *cobra.Command, cred *activeCredential, ids []int) map[int]string {
	cached := cache.Load(apiURL).Organisations
	m := make(map[int]string, len(ids))
	for _, id := range ids {
		name := cached[strconv.Itoa(id)]
		if name == "" {
			m = map[int]string{}
			break
		}
		m[id] = name
	}
	if len(m) == len(ids) {
		return m
	}
	orgs, err := cred.client().Organisations(cmd.Context())
	if err != nil {
		return m
	}
	rememberOrganisations(orgs)
	for _, o := range orgs {
		m[o.ID] = o.Name
	}
	return m
}

func orgLabel(names map[int]string, id int) string {
	return label(names[id], id)
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
			{Label: "Project", Value: label(p.Name, p.ID)},
			{Label: "Organisation", Value: orgLabel(orgLabels(cmd, cred, []int{p.Organisation}), p.Organisation)},
		})
	})
}

var projectListCmd = &cobra.Command{
	Use:   "list",
	Short: "List projects",
	Example: `  flagsmith project list

  # scope to one organisation
  flagsmith project list --organisation acme`,
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
		projects, err := cred.client().Projects(cmd.Context(), orgID)
		if err != nil {
			return err
		}
		return output.Render(cmd.OutOrStdout(), projects, outputOpts(), func(w io.Writer) error {
			if len(projects) == 0 {
				fmt.Fprintln(w, "No projects.")
				return nil
			}
			ids := make([]int, len(projects))
			for i, p := range projects {
				ids[i] = p.Organisation
			}
			names := orgLabels(cmd, cred, ids)
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
	Use:     "get <project>",
	Short:   "Show a project",
	Example: "  flagsmith project get acme-api",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, err := credentialContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveProjectRefID(cmd, cred, args[0])
		if err != nil {
			return err
		}
		p, err := cred.client().GetProject(cmd.Context(), id)
		if err != nil {
			return err
		}
		return renderProject(cmd, cred, p)
	},
}

var projectCreateCmd = &cobra.Command{
	Use:     "create <name>",
	Short:   "Create a project",
	Example: `  flagsmith project create acme-api --organisation acme`,
	Args:    cobra.ExactArgs(1),
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
		p, err := cred.client().CreateProject(cmd.Context(), body)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Created project %s", label(p.Name, p.ID))
		return renderProject(cmd, cred, p)
	},
}

var projectUpdateCmd = &cobra.Command{
	Use:   "update <project>",
	Short: "Update a project",
	Example: `  flagsmith project update acme-api --name acme-backend
  flagsmith project update acme-api --hide-disabled-flags`,
	Args: cobra.ExactArgs(1),
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
		p, err := cred.client().UpdateProject(cmd.Context(), id, body)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Updated project %s", label(p.Name, p.ID))
		return renderProject(cmd, cred, p)
	},
}

var projectDeleteCmd = &cobra.Command{
	Use:     "delete <project>",
	Short:   "Delete a project",
	Example: "  flagsmith project delete acme-api --yes",
	Args:    cobra.ExactArgs(1),
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
		if ok, err := confirmOrYes(cmd, fmt.Sprintf("delete project %s", label(args[0], id))); err != nil {
			return err
		} else if !ok {
			fmt.Fprintln(errOut, "Aborted; nothing deleted.")
			return nil
		}
		if err := cred.client().DeleteProject(cmd.Context(), id); err != nil {
			return err
		}
		output.Success(errOut, "Deleted project %s", label(args[0], id))
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
