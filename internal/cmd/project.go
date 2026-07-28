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

// resolveProjectRefID turns a project reference (id or name) into an id. A
// name resolves within the organisation context when one is set, and across every
// accessible project otherwise.
func resolveProjectRefID(cmd *cobra.Command, pc *projectContext, cred *activeCredential, ref string) (int, error) {
	if id, err := strconv.Atoi(ref); err == nil {
		return id, nil
	}
	orgID := 0
	notFound := fmt.Errorf("project %q not found", ref)
	if pc.Organisation.Value != nil {
		var err error
		if orgID, err = resolveOrganisationID(cmd, pc, cred); err != nil {
			return 0, err
		}
		notFound = fmt.Errorf("project %q not found in organisation %d", ref, orgID)
	}
	projects, err := cred.client().Projects(cmd.Context(), orgID)
	if err != nil {
		return 0, err
	}
	byID := idNameMap(projects, func(p api.Project) (string, string) { return strconv.Itoa(p.ID), p.Name })
	_ = cache.Merge(apiURL, &cache.Names{Projects: byID}) // opportunistic
	return resolveIDRef(cmd, "project", ref, byID, notFound, hintProjectList)
}

// orgLabels maps the given organisation ids to names for display, from the local
// name cache when it covers them all. Only a miss pays one Organisations fetch,
// which reseeds the cache; labels stay best-effort either way.
func orgLabels(cmd *cobra.Command, cred *activeCredential, ids []int) map[int]string {
	cached := cache.Load(apiURL).Organisations
	m := make(map[int]string, len(ids))
	for _, id := range ids {
		name := cached[strconv.Itoa(id)]
		if name == "" {
			m = nil // any miss falls through to one fetch; ids may repeat
			break
		}
		m[id] = name
	}
	if m != nil {
		return m
	}
	m = map[int]string{}
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
		if pc.Organisation.Value != nil {
			if orgID, err = resolveOrganisationID(cmd, pc, cred); err != nil {
				return err
			}
		}
		projects, err := cred.client().Projects(cmd.Context(), orgID)
		if err != nil {
			return err
		}
		ids := make([]int, len(projects))
		for i, p := range projects {
			ids[i] = p.Organisation
		}
		names := orgLabels(cmd, cred, ids)
		return renderList(cmd, projects, "No projects.",
			[]string{"NAME", "ID", "ORGANISATION"},
			func(_ int, p api.Project) []string {
				return []string{p.Name, strconv.Itoa(p.ID), orgLabel(names, p.Organisation)}
			}, "project", "projects")
	},
}

var projectGetCmd = &cobra.Command{
	Use:     "get <project>",
	Short:   "Show a project",
	Example: "  flagsmith project get acme-api",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pc, cred, err := credentialContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveProjectRefID(cmd, pc, cred, args[0])
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
		pc, cred, err := credentialContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveProjectRefID(cmd, pc, cred, args[0])
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
		pc, cred, err := credentialContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveProjectRefID(cmd, pc, cred, args[0])
		if err != nil {
			return err
		}
		// The ref's name half only — never the typed id — with the cached
		// display name (seeded by name resolution) filling in when known.
		name := cache.Load(apiURL).Projects[strconv.Itoa(id)]
		if name == "" {
			name = nameRef(args[0])
		}
		errOut := cmd.ErrOrStderr()
		if ok, err := confirmed(cmd, fmt.Sprintf("delete project %s", label(name, id)), "deleted"); !ok || err != nil {
			return err
		}
		if err := cred.client().DeleteProject(cmd.Context(), id); err != nil {
			return err
		}
		output.Success(errOut, "Deleted project %s", label(name, id))
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
