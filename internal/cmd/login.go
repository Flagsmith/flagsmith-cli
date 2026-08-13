package cmd

import (
	"context"
	"errors"
	"fmt"

	"github.com/blang/semver/v4"
	"github.com/pkg/browser"
	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/api"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/output"
)

// minOAuthVersion is the Flagsmith release that added the admin-api OAuth
// scope the CLI logs in with: https://github.com/Flagsmith/flagsmith/releases/tag/v2.255.0
const minOAuthVersion = "2.255.0"

var noBrowser bool

func newLoginCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Log in to Flagsmith in your browser",
		// The browser round-trip can take minutes; opt out of the overall
		// per-invocation deadline (auth.Login has its own loginTimeout).
		Annotations: map[string]string{annotationLongRunning: "true"},
		Example: `  flagsmith login

  # print the URL instead of opening a browser (headless)
  flagsmith login --no-browser`,
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

// tooOldForOAuthHint explains a missing discovery document when the instance
// turns out to be a Flagsmith predating the OAuth login, and reports "" — no
// hint, leaving the generic one — whenever that cannot be established. Only a
// self-hosted instance can be this old, so the Master API key it points at is
// named host-scoped. An instance reporting an image tag that is not a version
// ("latest", a commit sha) says nothing about its age: claiming it is too old
// would send a user with a healthy instance chasing an upgrade they don't need.
func tooOldForOAuthHint(ctx context.Context) string {
	tag, err := api.ServerVersion(ctx, sharedHTTPClient(), apiURL)
	if err != nil {
		return ""
	}
	got, err := semver.ParseTolerant(tag)
	if err != nil || got.GTE(semver.MustParse(minOAuthVersion)) {
		return ""
	}
	return fmt.Sprintf(
		"This instance runs Flagsmith %s; browser login needs %s or newer. Upgrade it, or set %s to a Master API key.",
		tag, minOAuthVersion, apiKeyVar())
}

func browserLogin(cmd *cobra.Command) error {
	// --no-input promises zero interaction; a browser login is nothing but
	// interaction. Master API keys go through FLAGSMITH_API_KEY. (--yes is
	// authorization, not a liveness switch, so it does not block login.)
	if noInput() {
		return withHint(errors.New("browser login needs a terminal and cannot run with --no-input"),
			hintMasterKey())
	}
	// The session lives in the OS keychain; without one, minting tokens we
	// can't store would strand a live session — fail closed toward the env var.
	if !auth.KeychainAvailable() {
		return withHint(errors.New("no OS keychain available to store the session"),
			hintMasterKey())
	}
	open := browser.OpenURL
	if noBrowser || !stdinIsTTY() {
		open = nil // without a TTY the CLI never opens a browser
	}
	creds, err := auth.Login(cmd.Context(), sharedHTTPClient(), apiURL, open, cmd.OutOrStdout())
	if err != nil {
		if errors.Is(err, auth.ErrNoDiscovery) {
			return withHint(err, tooOldForOAuthHint(cmd.Context()))
		}
		return err
	}
	if err := auth.Save(creds); err != nil {
		return fmt.Errorf("storing credentials: %w", err)
	}
	loggedIn := api.NewClient(creds.APIURL, api.Bearer(creds.AccessToken),
		api.WithHTTPClient(sharedHTTPClient()), api.WithUserAgent(userAgent()))
	user, err := loggedIn.UsersMe(cmd.Context())
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
