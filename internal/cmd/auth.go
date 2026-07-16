package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/auth"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Inspect and manage authentication",
}

// freshCredentials loads stored credentials, refreshing (and re-saving) the
// session if the access token is expired.
func freshCredentials(cmd *cobra.Command) (*auth.Credentials, string, error) {
	creds, source, err := auth.Load()
	if err != nil {
		return nil, "", err
	}
	creds, refreshed, err := auth.EnsureFresh(cmd.Context(), creds)
	if err != nil {
		return nil, "", err
	}
	if refreshed {
		if source, err = auth.Save(creds); err != nil {
			return nil, "", fmt.Errorf("storing refreshed credentials: %w", err)
		}
	}
	return creds, source, nil
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the current identity and credential source",
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, source, err := freshCredentials(cmd)
		if err != nil {
			return err
		}
		user, err := api.UsersMe(cmd.Context(), creds.APIURL, creds.AccessToken)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "✓ Logged in to %s as %s\n", creds.APIURL, user.Email)
		fmt.Fprintf(out, "  Credential source: %s\n", source)
		fmt.Fprintf(out, "  Access token expires: %s\n",
			creds.ExpiresAt.Local().Format(time.RFC1123))
		return nil
	},
}

var authTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Print a valid access token (for curl and scripts)",
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, _, err := freshCredentials(cmd)
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), creds.AccessToken)
		return nil
	},
}

func init() {
	authCmd.AddCommand(authStatusCmd, authTokenCmd)
	rootCmd.AddCommand(authCmd)
}
