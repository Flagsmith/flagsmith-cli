package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/output"
)

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "logout",
		Short:       "Log out of an instance and revoke the stored session",
		Annotations: map[string]string{annotationLongRunning: "true"},
		Example:     "  flagsmith logout",
		RunE:        runLogout,
	}
}

func runLogout(cmd *cobra.Command, args []string) error {
	if _, err := applyContext(cmd); err != nil {
		return err
	}
	creds, err := auth.Load(apiURL)
	if errors.Is(err, auth.ErrNotLoggedIn) {
		fmt.Fprintln(cmd.ErrOrStderr(), "Not logged in.")
		return nil
	}
	if err != nil {
		return err
	}
	if creds.EffectiveKind() == auth.KindOAuth {
		if err := auth.Revoke(cmd.Context(), sharedHTTPClient(), creds); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(),
				"Warning: could not revoke session server-side: %v\n", err)
		}
	}
	if err := auth.Delete(apiURL); err != nil {
		return fmt.Errorf("removing stored credentials: %w", err)
	}
	output.Success(cmd.ErrOrStderr(), "Logged out of %s", creds.APIURL)
	return nil
}

func init() {
	rootCmd.AddCommand(newLogoutCmd())
	authCmd.AddCommand(newLogoutCmd())
}
