package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/internal/cache"
	"github.com/Flagsmith/flagsmith-cli/internal/output"
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
		// Rotated sessions persist to the store they were loaded from —
		// refresh never migrates credentials between stores.
		if err := auth.Save(creds, source); err != nil {
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

// sourceLabel renders a credential source for humans; the plaintext file
// store is always labelled as such.
func sourceLabel(source string) string {
	if source == auth.SourceFile {
		return "file (plaintext)"
	}
	return source
}

// rememberOrganisations opportunistically seeds the name cache.
func rememberOrganisations(orgs []api.Organisation) {
	names := map[string]string{}
	for _, o := range orgs {
		names[strconv.Itoa(o.ID)] = o.Name
	}
	_ = cache.Merge(apiURL, &cache.Names{Organisations: names})
}

func orgList(orgs []api.Organisation) string {
	parts := make([]string, len(orgs))
	for i, o := range orgs {
		parts[i] = fmt.Sprintf("%s (%d)", o.Name, o.ID)
	}
	return strings.Join(parts, ", ")
}

// warnPlaintext reminds the user that the opt-in file store is plaintext.
func warnPlaintext(cmd *cobra.Command, source string) {
	if source != auth.SourceFile {
		return
	}
	path, err := auth.CredentialsFilePath()
	if err != nil {
		path = "the credentials file"
	}
	fmt.Fprintf(cmd.ErrOrStderr(),
		"Warning: credentials stored in plaintext at %s\n", path)
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the current identity, organisation and credential source",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := applyContext(cmd); err != nil {
			return err
		}
		cred, err := resolveCredential(cmd.Context())
		if err != nil {
			return err
		}
		orgs, err := api.Organisations(cmd.Context(), apiURL, cred.auth)
		if err != nil {
			return err
		}
		rememberOrganisations(orgs)

		email := ""
		if cred.kind != auth.KindMaster {
			user, err := api.UsersMe(cmd.Context(), apiURL, cred.auth)
			if err != nil {
				return err
			}
			email = user.Email
		}
		status := struct {
			APIURL        string             `json:"apiUrl"`
			Kind          auth.Kind          `json:"kind"`
			Email         string             `json:"email,omitempty"`
			Organisations []api.Organisation `json:"organisations"`
			Source        string             `json:"credentialSource"`
			ExpiresAt     string             `json:"expiresAt,omitempty"`
		}{APIURL: apiURL, Kind: cred.kind, Email: email, Organisations: orgs, Source: sourceLabel(cred.source)}
		if !cred.expires.IsZero() {
			status.ExpiresAt = cred.expires.Format(time.RFC3339)
		}

		return output.Render(cmd.OutOrStdout(), status, outputOpts(), func(w io.Writer) error {
			identity := "Master API key"
			if email != "" {
				identity = email
			}
			fields := []output.Field{
				{Label: "Instance", Value: apiURL},
				{Label: "Identity", Value: identity},
				{Label: "Organisations", Value: orgList(orgs)},
				{Label: "Credential source", Value: sourceLabel(cred.source)},
			}
			if !cred.expires.IsZero() {
				fields = append(fields, output.Field{
					Label: "Access token expires",
					Value: cred.expires.Local().Format(time.RFC1123),
				})
			}
			return output.Detail(w, fields)
		})
	},
}

var authTokenCmd = &cobra.Command{
	Use:   "token",
	Short: "Print the active Admin API credential (for curl and scripts)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := applyContext(cmd); err != nil {
			return err
		}
		cred, err := resolveCredential(cmd.Context())
		if err != nil {
			return err
		}
		return output.Render(cmd.OutOrStdout(),
			map[string]string{"token": cred.token}, outputOpts(),
			func(w io.Writer) error {
				_, err := fmt.Fprintln(w, cred.token)
				return err
			})
	},
}

func init() {
	authCmd.AddCommand(authStatusCmd, authTokenCmd)
	rootCmd.AddCommand(authCmd)
}
