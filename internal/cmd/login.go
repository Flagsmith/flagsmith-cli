package cmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/pkg/browser"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/auth"
)

var (
	noBrowser       bool
	loginToken      bool
	loginTokenStdin bool
)

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to Flagsmith in your browser, or store a Master API key",
	RunE: func(cmd *cobra.Command, args []string) error {
		if loginToken || loginTokenStdin {
			return masterKeyLogin(cmd)
		}
		return browserLogin(cmd)
	},
}

func browserLogin(cmd *cobra.Command) error {
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
	warnPlaintext(cmd, source)
	user, err := api.UsersMe(cmd.Context(), creds.APIURL, api.Bearer(creds.AccessToken))
	if err != nil {
		return fmt.Errorf("logged in, but fetching identity failed: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(),
		"✓ Logged in to %s as %s\n  Credentials stored in %s.\n",
		creds.APIURL, user.Email, source)
	return nil
}

func masterKeyLogin(cmd *cobra.Command) error {
	key, err := readMasterKey(cmd)
	if err != nil {
		return err
	}
	kind, err := auth.ClassifyAPIKey(key)
	if err != nil {
		return err
	}
	if kind != auth.KindMaster {
		return errors.New(
			"that doesn't look like a Master API key (expected {prefix}.{secret}) — for browser login, run `flagsmith login` without --token")
	}
	orgs, err := api.Organisations(cmd.Context(), apiURL, api.APIKey(key))
	if err != nil {
		return fmt.Errorf("verifying the key against %s: %w", apiURL, err)
	}
	source, err := auth.Save(&auth.Credentials{
		Kind:      auth.KindMaster,
		APIURL:    strings.TrimRight(apiURL, "/"),
		MasterKey: key,
	})
	if err != nil {
		return fmt.Errorf("storing credentials: %w", err)
	}
	warnPlaintext(cmd, source)
	fmt.Fprintf(cmd.OutOrStdout(),
		"✓ Authenticated to %s with a Master API key (organisation: %s)\n  Credentials stored in %s.\n",
		apiURL, orgList(orgs), source)
	return nil
}

func readMasterKey(cmd *cobra.Command) (string, error) {
	if loginTokenStdin {
		b, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		return "", errors.New("stdin is not a TTY — pipe the key with --token-stdin instead")
	}
	fmt.Fprint(cmd.OutOrStdout(), "Paste your Master API key: ")
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(cmd.OutOrStdout())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func init() {
	loginCmd.Flags().BoolVar(&noBrowser, "no-browser", false,
		"print the login URL instead of opening a browser")
	loginCmd.Flags().BoolVar(&loginToken, "token", false,
		"paste a Master API key instead of using the browser")
	loginCmd.Flags().BoolVar(&loginTokenStdin, "token-stdin", false,
		"read a Master API key from stdin")
	rootCmd.AddCommand(loginCmd)
}
