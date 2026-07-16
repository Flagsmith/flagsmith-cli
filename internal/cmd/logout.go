package cmd

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/auth"
)

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Log out and revoke the stored session",
	RunE: func(cmd *cobra.Command, args []string) error {
		creds, _, err := auth.Load()
		if errors.Is(err, auth.ErrNotLoggedIn) {
			fmt.Fprintln(cmd.OutOrStdout(), "Not logged in.")
			return nil
		}
		if err != nil {
			return err
		}
		if err := auth.Revoke(cmd.Context(), creds); err != nil {
			fmt.Fprintf(cmd.OutOrStdout(),
				"Warning: could not revoke session server-side: %v\n", err)
		}
		if err := auth.Delete(); err != nil {
			return fmt.Errorf("removing stored credentials: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "✓ Logged out of %s\n", creds.APIURL)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(logoutCmd)
}
