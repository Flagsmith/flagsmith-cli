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

var organisationCmd = &cobra.Command{
	Use:     "organisation",
	Aliases: []string{"org"},
	Short:   "Manage organisations",
}

var (
	orgNameFlag         string
	orgForce2FAFlag     bool
	orgWebhookEmailFlag string
)

// credentialContext resolves the invocation context and credential without
// demanding a project — for organisation commands and for project commands,
// whose organisation scope is optional.
func credentialContext(cmd *cobra.Command) (*projectContext, *activeCredential, error) {
	pc, err := applyContext(cmd)
	if err != nil {
		return nil, nil, err
	}
	cred, err := resolveCredential(cmd.Context())
	if err != nil {
		return nil, nil, err
	}
	return pc, cred, nil
}

// resolveOrganisationRefID turns an organisation reference (id or name) into an id.
func resolveOrganisationRefID(cmd *cobra.Command, cred *activeCredential, ref string) (int, error) {
	if id, err := strconv.Atoi(ref); err == nil {
		return id, nil
	}
	orgs, err := cred.client().Organisations(cmd.Context())
	if err != nil {
		return 0, err
	}
	rememberOrganisations(orgs)
	byID := idNameMap(orgs, func(o api.Organisation) (string, string) { return strconv.Itoa(o.ID), o.Name })
	return resolveIDRef(cmd, "organisation", ref, byID,
		fmt.Errorf("organisation %q not found", ref),
		hintOrganisationList)
}

// orgBodyFromFlags collects the organisation fields the user set.
func orgBodyFromFlags(cmd *cobra.Command) map[string]any {
	body := map[string]any{}
	if cmd.Flags().Changed("name") {
		body["name"] = orgNameFlag
	}
	if cmd.Flags().Changed("force-2fa") {
		body["force_2fa"] = orgForce2FAFlag
	}
	if cmd.Flags().Changed("webhook-email") {
		body["webhook_notification_email"] = orgWebhookEmailFlag
	}
	return body
}

func renderOrganisation(cmd *cobra.Command, o *api.Organisation) error {
	return output.Render(cmd.OutOrStdout(), o, outputOpts(), func(w io.Writer) error {
		return output.Detail(w, []output.Field{
			{Label: "Organisation", Value: label(o.Name, o.ID)},
		})
	})
}

var organisationListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List organisations",
	Example: "  flagsmith organisation list",
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cred, err := credentialContext(cmd)
		if err != nil {
			return err
		}
		orgs, err := cred.client().Organisations(cmd.Context())
		if err != nil {
			return err
		}
		return renderList(cmd, orgs, "No organisations.",
			[]string{"NAME", "ID"},
			func(_ int, o api.Organisation) []string {
				return []string{o.Name, strconv.Itoa(o.ID)}
			}, "organisation", "organisations")
	},
}

var organisationGetCmd = &cobra.Command{
	Use:     "get <organisation>",
	Short:   "Show an organisation",
	Example: "  flagsmith organisation get acme",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cred, err := credentialContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveOrganisationRefID(cmd, cred, args[0])
		if err != nil {
			return err
		}
		org, err := cred.client().GetOrganisation(cmd.Context(), id)
		if err != nil {
			return err
		}
		return renderOrganisation(cmd, org)
	},
}

var organisationCreateCmd = &cobra.Command{
	Use:     "create <name>",
	Short:   "Create an organisation",
	Example: `  flagsmith organisation create "Acme Inc"`,
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cred, err := credentialContext(cmd)
		if err != nil {
			return err
		}
		body := orgBodyFromFlags(cmd)
		body["name"] = args[0]
		org, err := cred.client().CreateOrganisation(cmd.Context(), body)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Created organisation %s", label(org.Name, org.ID))
		return renderOrganisation(cmd, org)
	},
}

var organisationUpdateCmd = &cobra.Command{
	Use:   "update <organisation>",
	Short: "Update an organisation",
	Example: `  flagsmith organisation update acme --name "Acme Corp"
  flagsmith organisation update acme --force-2fa`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		body := orgBodyFromFlags(cmd)
		if len(body) == 0 {
			return usageErrorf("nothing to update — pass --name, --force-2fa, or --webhook-email")
		}
		_, cred, err := credentialContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveOrganisationRefID(cmd, cred, args[0])
		if err != nil {
			return err
		}
		org, err := cred.client().UpdateOrganisation(cmd.Context(), id, body)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Updated organisation %s", label(org.Name, org.ID))
		return renderOrganisation(cmd, org)
	},
}

var organisationDeleteCmd = &cobra.Command{
	Use:     "delete <organisation>",
	Short:   "Delete an organisation",
	Example: "  flagsmith organisation delete acme --yes",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		_, cred, err := credentialContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveOrganisationRefID(cmd, cred, args[0])
		if err != nil {
			return err
		}
		// The ref's name half only — never the typed id — with the cached
		// display name (seeded by name resolution) filling in when known.
		name := cache.Load(apiURL).Organisations[strconv.Itoa(id)]
		if name == "" {
			name = nameRef(args[0])
		}
		errOut := cmd.ErrOrStderr()
		if ok, err := confirmed(cmd, fmt.Sprintf("delete organisation %s", label(name, id)), "deleted"); !ok || err != nil {
			return err
		}
		if err := cred.client().DeleteOrganisation(cmd.Context(), id); err != nil {
			return err
		}
		output.Success(errOut, "Deleted organisation %s", label(name, id))
		return nil
	},
}

func init() {
	for _, c := range []*cobra.Command{organisationCreateCmd, organisationUpdateCmd} {
		c.Flags().BoolVar(&orgForce2FAFlag, "force-2fa", false, "require 2FA for all members")
		c.Flags().StringVar(&orgWebhookEmailFlag, "webhook-email", "", "webhook notification email")
	}
	organisationUpdateCmd.Flags().StringVar(&orgNameFlag, "name", "", "rename the organisation")
	organisationCmd.AddCommand(organisationListCmd, organisationGetCmd, organisationCreateCmd, organisationUpdateCmd, organisationDeleteCmd)
	rootCmd.AddCommand(organisationCmd)
}
