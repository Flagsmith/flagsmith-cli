package cmd

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
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

// credentialContext resolves just the credential (and points the auth layer at
// the instance) — organisation commands need no project.
func credentialContext(cmd *cobra.Command) (*activeCredential, error) {
	if _, err := applyContext(cmd); err != nil {
		return nil, err
	}
	return resolveCredential(cmd.Context())
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
	byID := make(map[string]string, len(orgs))
	for _, o := range orgs {
		byID[strconv.Itoa(o.ID)] = o.Name
	}
	hits := matchByName(byID, ref)
	if len(hits) == 0 {
		return 0, withHint(
			fmt.Errorf("organisation %q not found", ref),
			hintOrganisationList)
	}
	chosen, err := pickCandidate(cmd, "organisation", "id", ref, hits, byID)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(chosen)
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
			{Label: "Organisation", Value: fmt.Sprintf("%s (%d)", o.Name, o.ID)},
		})
	})
}

var organisationListCmd = &cobra.Command{
	Use:     "list",
	Short:   "List organisations",
	Example: "  flagsmith organisation list",
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, err := credentialContext(cmd)
		if err != nil {
			return err
		}
		orgs, err := cred.client().Organisations(cmd.Context())
		if err != nil {
			return err
		}
		return output.Render(cmd.OutOrStdout(), orgs, outputOpts(), func(w io.Writer) error {
			if len(orgs) == 0 {
				fmt.Fprintln(w, "No organisations.")
				return nil
			}
			rows := make([][]string, len(orgs))
			for i, o := range orgs {
				rows[i] = []string{o.Name, strconv.Itoa(o.ID)}
			}
			if err := output.Table(w, []string{"NAME", "ID"}, rows); err != nil {
				return err
			}
			fmt.Fprintf(w, "\n%d %s\n", len(orgs), plural(len(orgs), "organisation", "organisations"))
			return nil
		})
	},
}

var organisationGetCmd = &cobra.Command{
	Use:     "get <organisation>",
	Short:   "Show an organisation",
	Example: "  flagsmith organisation get acme",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, err := credentialContext(cmd)
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
		cred, err := credentialContext(cmd)
		if err != nil {
			return err
		}
		body := orgBodyFromFlags(cmd)
		body["name"] = args[0]
		org, err := cred.client().CreateOrganisation(cmd.Context(), body)
		if err != nil {
			return err
		}
		output.Success(cmd.ErrOrStderr(), "Created organisation %s (%d)", org.Name, org.ID)
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
		cred, err := credentialContext(cmd)
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
		output.Success(cmd.ErrOrStderr(), "Updated organisation %s (%d)", org.Name, org.ID)
		return renderOrganisation(cmd, org)
	},
}

var organisationDeleteCmd = &cobra.Command{
	Use:     "delete <organisation>",
	Short:   "Delete an organisation",
	Example: "  flagsmith organisation delete acme --yes",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, err := credentialContext(cmd)
		if err != nil {
			return err
		}
		id, err := resolveOrganisationRefID(cmd, cred, args[0])
		if err != nil {
			return err
		}
		errOut := cmd.ErrOrStderr()
		if ok, err := confirmOrYes(cmd, fmt.Sprintf("delete organisation %s (%d)", args[0], id)); err != nil {
			return err
		} else if !ok {
			fmt.Fprintln(errOut, "Aborted; nothing deleted.")
			return nil
		}
		if err := cred.client().DeleteOrganisation(cmd.Context(), id); err != nil {
			return err
		}
		output.Success(errOut, "Deleted organisation %s (%d)", args[0], id)
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
