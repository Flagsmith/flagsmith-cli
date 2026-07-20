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
	insecureStorage bool
)

// credentialStore picks where login stores credentials: the OS keychain, or
// — only with explicit --insecure-storage opt-in — the plaintext file. With
// no keychain and no opt-in it fails closed before any flow starts.
func credentialStore() (string, error) {
	if insecureStorage {
		return auth.SourceFile, nil
	}
	if !auth.KeychainAvailable() {
		path, err := auth.CredentialsFilePath()
		if err != nil {
			path = "a plaintext file"
		}
		return "", fmt.Errorf(
			"no OS keychain available — set FLAGSMITH_API_KEY, or re-run with --insecure-storage to store credentials in plaintext at %s", path)
	}
	return auth.SourceKeychain, nil
}

func newLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to Flagsmith in your browser, or store a Master API key",
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := applyContext(cmd); err != nil {
				return err
			}
			if loginToken || loginTokenStdin {
				return masterKeyLogin(cmd)
			}
			return browserLogin(cmd)
		},
	}
	cmd.Flags().BoolVar(&noBrowser, "no-browser", false,
		"print the login URL instead of opening a browser")
	cmd.Flags().BoolVar(&loginToken, "token", false,
		"paste a Master API key instead of using the browser")
	cmd.Flags().BoolVar(&loginTokenStdin, "token-stdin", false,
		"read a Master API key from stdin")
	cmd.Flags().BoolVar(&insecureStorage, "insecure-storage", false,
		"store credentials in a plaintext file instead of the OS keychain")
	return cmd
}

func browserLogin(cmd *cobra.Command) error {
	// Resolve storage before starting the flow — minting tokens we can't
	// store would strand a live session.
	store, err := credentialStore()
	if err != nil {
		return err
	}
	open := browser.OpenURL
	if noBrowser {
		open = nil
	}
	creds, err := auth.Login(cmd.Context(), apiURL, open, cmd.OutOrStdout())
	if err != nil {
		return err
	}
	if err := auth.Save(creds, store); err != nil {
		return fmt.Errorf("storing credentials: %w", err)
	}
	warnPlaintext(cmd, store)
	user, err := api.UsersMe(cmd.Context(), creds.APIURL, api.Bearer(creds.AccessToken))
	if err != nil {
		return fmt.Errorf("logged in, but fetching identity failed: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(),
		"✓ Logged in to %s as %s\n  Credentials stored in %s.\n",
		creds.APIURL, user.Email, sourceLabel(store))
	return nil
}

func masterKeyLogin(cmd *cobra.Command) error {
	store, err := credentialStore()
	if err != nil {
		return err
	}
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
	if err := auth.Save(&auth.Credentials{
		Kind:      auth.KindMaster,
		APIURL:    strings.TrimRight(apiURL, "/"),
		MasterKey: key,
	}, store); err != nil {
		return fmt.Errorf("storing credentials: %w", err)
	}
	warnPlaintext(cmd, store)
	fmt.Fprintf(cmd.OutOrStdout(),
		"✓ Authenticated to %s with a Master API key (organisation: %s)\n  Credentials stored in %s.\n",
		apiURL, orgList(orgs), sourceLabel(store))
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
	rootCmd.AddCommand(newLoginCmd())
	authCmd.AddCommand(newLoginCmd())
}
