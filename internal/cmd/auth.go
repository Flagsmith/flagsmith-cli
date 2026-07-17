package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/auth"
)

const envAPIKey = "FLAGSMITH_API_KEY"

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Inspect and manage authentication",
}

// activeCredential is the resolved Admin API credential for this invocation.
type activeCredential struct {
	kind    auth.Kind
	auth    api.Auth
	token   string
	source  string
	expires time.Time // zero when not applicable
}

// resolveCredential applies the credential precedence chain:
// $FLAGSMITH_API_KEY first, then the stored login for the instance
// (refreshing and re-saving the session when expired).
func resolveCredential(ctx context.Context) (*activeCredential, error) {
	if v := os.Getenv(envAPIKey); v != "" {
		kind, err := auth.ClassifyAPIKey(v)
		if err != nil {
			return nil, err
		}
		cred := &activeCredential{kind: kind, token: v, source: "$" + envAPIKey}
		if kind == auth.KindMaster {
			cred.auth = api.APIKey(v)
		} else {
			cred.auth = api.Bearer(v)
		}
		return cred, nil
	}

	creds, source, err := auth.Load(apiURL)
	if err != nil {
		return nil, err
	}
	creds, refreshed, err := auth.EnsureFresh(ctx, creds)
	if err != nil {
		return nil, err
	}
	if refreshed {
		if source, err = auth.Save(creds); err != nil {
			return nil, fmt.Errorf("storing refreshed credentials: %w", err)
		}
	}
	cred := &activeCredential{
		kind:    creds.EffectiveKind(),
		token:   creds.Token(),
		source:  source,
		expires: creds.ExpiresAt,
	}
	if cred.kind == auth.KindMaster {
		cred.auth = api.APIKey(cred.token)
	} else {
		cred.auth = api.Bearer(cred.token)
	}
	return cred, nil
}

func orgList(orgs []api.Organisation) string {
	parts := make([]string, len(orgs))
	for i, o := range orgs {
		parts[i] = fmt.Sprintf("%s (%d)", o.Name, o.ID)
	}
	return strings.Join(parts, ", ")
}

// warnPlaintext tells the user when credentials had to bypass the keychain.
func warnPlaintext(cmd *cobra.Command, source string) {
	if source != auth.SourceFile {
		return
	}
	path, err := auth.CredentialsFilePath()
	if err != nil {
		path = "the credentials file"
	}
	fmt.Fprintf(cmd.ErrOrStderr(),
		"Warning: OS keychain unavailable — credentials stored in plaintext at %s\n", path)
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the current identity, organisation and credential source",
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, err := resolveCredential(cmd.Context())
		if err != nil {
			return err
		}
		orgs, err := api.Organisations(cmd.Context(), apiURL, cred.auth)
		if err != nil {
			return err
		}
		out := cmd.OutOrStdout()
		if cred.kind == auth.KindMaster {
			fmt.Fprintf(out, "✓ Authenticated to %s with a Master API key\n", apiURL)
		} else {
			user, err := api.UsersMe(cmd.Context(), apiURL, cred.auth)
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "✓ Logged in to %s as %s\n", apiURL, user.Email)
		}
		fmt.Fprintf(out, "  Organisations: %s\n", orgList(orgs))
		fmt.Fprintf(out, "  Credential source: %s\n", cred.source)
		if !cred.expires.IsZero() {
			fmt.Fprintf(out, "  Access token expires: %s\n",
				cred.expires.Local().Format(time.RFC1123))
		}
		return nil
	},
}

var authTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Print the active Admin API credential (for curl and scripts)",
	RunE: func(cmd *cobra.Command, args []string) error {
		cred, err := resolveCredential(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), cred.token)
		return nil
	},
}

func init() {
	authCmd.AddCommand(authStatusCmd, authTokenCmd)
	rootCmd.AddCommand(authCmd)
}
