package cmd

import (
	"fmt"

	"github.com/pkg/browser"
	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/auth"
)

var noBrowser bool

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to Flagsmith in your browser",
	RunE: func(cmd *cobra.Command, args []string) error {
		open := browser.OpenURL
		if noBrowser {
			open = nil
		}
		creds, err := auth.Login(cmd.Context(), apiURL, open, cmd.OutOrStdout())
		if err != nil {
			return err
		}
		source, err := auth.Save(creds)
		if err != nil {
			return fmt.Errorf("storing credentials: %w", err)
		}
		user, err := api.UsersMe(cmd.Context(), creds.APIURL, creds.AccessToken)
		if err != nil {
			return fmt.Errorf("logged in, but fetching identity failed: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(),
			"✓ Logged in to %s as %s\n  Credentials stored in %s.\n",
			creds.APIURL, user.Email, source)
		return nil
	},
}

func init() {
	loginCmd.Flags().BoolVar(&noBrowser, "no-browser", false,
		"print the login URL instead of opening a browser")
	rootCmd.AddCommand(loginCmd)
}
