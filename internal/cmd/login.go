package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/pkg/browser"
	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

var noBrowser bool

func newLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to Flagsmith in your browser",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := applyContext(cmd); err != nil {
				return err
			}
			return browserLogin(cmd)
		},
	}
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false,
		"print the login URL instead of opening a browser")
	return cmd
}

func browserLogin(cmd *cobra.Command) error {
	// --yes/--no-input promises zero interaction; a browser login is
	// nothing but interaction. Master API keys go through FLAGSMITH_API_KEY.
	if yesFlag || os.Getenv("FLAGSMITH_NO_INPUT") != "" {
		return errors.New(
			"browser login needs a terminal and cannot run with --no-input/--yes — set FLAGSMITH_API_KEY to use a Master API key instead")
	}
	// The session lives in the OS keychain; without one, minting tokens we
	// can't store would strand a live session — fail closed toward the env var.
	if !auth.KeychainAvailable() {
		return errors.New(
			"no OS keychain available to store the session — set FLAGSMITH_API_KEY to use a Master API key instead")
	}
	open := browser.OpenURL
	if noBrowser || !stdinIsTTY() {
		open = nil // without a TTY the CLI never opens a browser (02)
	}
	creds, err := auth.Login(cmd.Context(), apiURL, open, cmd.OutOrStdout())
	if err != nil {
		return err
	}
	if err := auth.Save(creds); err != nil {
		return fmt.Errorf("storing credentials: %w", err)
	}
	user, err := api.UsersMe(cmd.Context(), creds.APIURL, api.Bearer(creds.AccessToken))
	if err != nil {
		return fmt.Errorf("logged in, but fetching identity failed: %w", err)
	}
	errOut := cmd.ErrOrStderr()
	output.Success(errOut, "Logged in to %s as %s", creds.APIURL, user.Email)
	fmt.Fprintln(errOut, "  Credentials stored in the OS keychain.")
	return nil
}

func init() {
	rootCmd.AddCommand(newLoginCmd())
	authCmd.AddCommand(newLoginCmd())
}
