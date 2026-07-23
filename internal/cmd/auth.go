package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/internal/cache"
	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

const (
	envAPIKey      = "FLAGSMITH_API_KEY"
	envAccessToken = "FLAGSMITH_ACCESS_TOKEN"
)

// credMu guards a per-invocation memo of the resolved credential, keyed by
// instance URL. It collapses concurrent resolutions into one: the first caller
// does the load-refresh-save under the lock (so an expired OAuth token is
// refreshed exactly once, with a single rotated refresh token saved), and
// later callers get the cached result. resetCredentialCache clears it at the
// start of each command run.
var (
	credMu    sync.Mutex
	credCache = map[string]*activeCredential{}
)

func resetCredentialCache() {
	credMu.Lock()
	credCache = map[string]*activeCredential{}
	credMu.Unlock()
}

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Inspect and manage authentication",
}

// activeCredential is the resolved Admin API credential for this invocation.
type activeCredential struct {
	kind      auth.Kind
	auth      api.Auth
	token     string
	source    string
	expires   time.Time   // zero when not applicable
	apiClient *api.Client // built once in loadCredential, before the cred is shared
}

// client returns the Admin API client for this credential. It is built eagerly
// in loadCredential (before the cred is memoised and shared across goroutines),
// so reading it here needs no lock.
func (c *activeCredential) client() *api.Client {
	return c.apiClient
}

// resolveCredential returns the Admin API credential for the current
// instance, memoised for the invocation. Concurrent callers serialise on
// credMu so an expired OAuth session refreshes exactly once; only successful
// results are cached, so a login performed mid-invocation is still picked up.
func resolveCredential(ctx context.Context) (*activeCredential, error) {
	credMu.Lock()
	defer credMu.Unlock()
	if c, ok := credCache[apiURL]; ok {
		return c, nil
	}
	c, err := loadCredential(ctx)
	if err != nil {
		return nil, err
	}
	credCache[apiURL] = c
	return c, nil
}

// loadCredential applies the credential precedence chain:
// $FLAGSMITH_API_KEY (Master API key), then $FLAGSMITH_ACCESS_TOKEN
// (OAuth-style bearer, OIDC-exchanged in CI), then the stored login for the
// instance (refreshing and re-saving the session when expired). Each env var
// maps to exactly one credential kind — no shape-guessing.
func loadCredential(ctx context.Context) (*activeCredential, error) {
	if v := os.Getenv(envAPIKey); v != "" {
		if err := auth.ValidateMasterKey(v); err != nil {
			return nil, err
		}
		cred := &activeCredential{kind: auth.KindMaster, token: v, source: "$" + envAPIKey, auth: api.APIKey(v)}
		cred.apiClient = newAPIClient(cred.auth)
		return cred, nil
	}

	if v := os.Getenv(envAccessToken); v != "" {
		cred := &activeCredential{kind: auth.KindBearer, token: v, source: "$" + envAccessToken, auth: api.Bearer(v)}
		cred.apiClient = newAPIClient(cred.auth)
		return cred, nil
	}

	creds, err := auth.Load(apiURL)
	if err != nil {
		return nil, err
	}
	creds, refreshed, err := auth.EnsureFresh(ctx, sharedHTTPClient(), creds)
	if err != nil {
		return nil, err
	}
	if refreshed {
		if err := auth.Save(creds); err != nil {
			return nil, fmt.Errorf("storing refreshed credentials: %w", err)
		}
	}
	cred := &activeCredential{
		kind:    creds.EffectiveKind(),
		token:   creds.Token(),
		source:  auth.SourceKeychain,
		expires: creds.ExpiresAt,
	}
	if cred.kind == auth.KindMaster {
		cred.auth = api.APIKey(cred.token)
	} else {
		cred.auth = api.Bearer(cred.token)
	}
	cred.apiClient = newAPIClient(cred.auth)
	return cred, nil
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

var authStatusCmd = &cobra.Command{
	Use:     "status",
	Short:   "Show the current identity, organisation and credential source",
	Example: "  flagsmith auth status",
	RunE: func(cmd *cobra.Command, args []string) error {
		if _, err := applyContext(cmd); err != nil {
			return err
		}
		cred, err := resolveCredential(cmd.Context())
		if err != nil {
			return err
		}
		orgs, err := cred.client().Organisations(cmd.Context())
		if err != nil {
			return err
		}
		rememberOrganisations(orgs)

		email := ""
		if cred.kind != auth.KindMaster {
			user, err := cred.client().UsersMe(cmd.Context())
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
		}{APIURL: apiURL, Kind: cred.kind, Email: email, Organisations: orgs, Source: cred.source}
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
				{Label: "Credential source", Value: cred.source},
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
	Example: `  flagsmith auth token

  # e.g. drive curl with it
  curl -H "Authorization: Api-Key $(flagsmith auth token)" \
    https://api.flagsmith.com/api/v1/organisations/`,
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
