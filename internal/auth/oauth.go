package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// ClientID is the first-party public OAuth client pre-registered on every
	// Flagsmith instance.
	ClientID = "flagsmith-cli"

	// Always request scope explicitly.
	Scope = "admin-api"

	loginTimeout = 5 * time.Minute
)

// Metadata is the subset of RFC 8414 authorization server metadata we use.
type Metadata struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RevocationEndpoint    string `json:"revocation_endpoint"`
}

func Discover(ctx context.Context, apiURL string) (*Metadata, error) {
	u := strings.TrimRight(apiURL, "/") + "/.well-known/oauth-authorization-server"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reaching %s: %w", u, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s — is this a Flagsmith API URL?", u, resp.Status)
	}
	var md Metadata
	if err := json.NewDecoder(resp.Body).Decode(&md); err != nil {
		return nil, fmt.Errorf("decoding authorization server metadata: %w", err)
	}
	if md.AuthorizationEndpoint == "" || md.TokenEndpoint == "" {
		return nil, errors.New("authorization server metadata is missing required endpoints")
	}
	return &md, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

func postToken(ctx context.Context, endpoint string, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		var oauthErr struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		if json.Unmarshal(body, &oauthErr) == nil && oauthErr.Error != "" {
			if oauthErr.Description != "" {
				return nil, fmt.Errorf("%s: %s", oauthErr.Error, oauthErr.Description)
			}
			return nil, errors.New(oauthErr.Error)
		}
		return nil, fmt.Errorf("token endpoint returned %s", resp.Status)
	}
	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("decoding token response: %w", err)
	}
	return &tok, nil
}

func randomURLSafe(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is not recoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Login runs the authorization-code + PKCE flow on a loopback listener.
// openBrowser may be nil (--no-browser); the URL is always written to out.
func Login(ctx context.Context, apiURL string, openBrowser func(string) error, out io.Writer) (*Credentials, error) {
	md, err := Discover(ctx, apiURL)
	if err != nil {
		return nil, err
	}

	verifier := randomURLSafe(32)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	state := randomURLSafe(16)

	// Literal 127.0.0.1, never "localhost": the server's RFC 8252 loopback
	// port-wildcard matching only applies to literal loopback IPs.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("starting loopback listener: %w", err)
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", ln.Addr().(*net.TCPAddr).Port)

	authURL := md.AuthorizationEndpoint + "?" + url.Values{
		"client_id":             {ClientID},
		"response_type":         {"code"},
		"scope":                 {Scope},
		"redirect_uri":          {redirectURI},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}.Encode()

	type callback struct {
		code string
		err  error
	}
	results := make(chan callback, 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			results <- callback{err: errors.New("state mismatch in callback — possible interception, aborting")}
			return
		}
		if errCode := q.Get("error"); errCode != "" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, resultPage("Login failed", "You can close this tab and return to your terminal."))
			results <- callback{err: fmt.Errorf("authorization failed: %s", errCode)}
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, resultPage("Login complete", "You can close this tab and return to your terminal."))
		results <- callback{code: q.Get("code")}
	})
	server := &http.Server{Handler: mux}
	go server.Serve(ln) //nolint:errcheck // returns ErrServerClosed on shutdown
	defer server.Shutdown(context.Background())

	fmt.Fprintf(out, "Log in to Flagsmith in your browser:\n\n  %s\n\n", authURL)
	if openBrowser != nil {
		if err := openBrowser(authURL); err != nil {
			fmt.Fprintf(out, "Could not open a browser automatically — open the URL above manually.\n\n")
		}
	}

	var code string
	select {
	case r := <-results:
		if r.err != nil {
			return nil, r.err
		}
		code = r.code
	case <-time.After(loginTimeout):
		return nil, errors.New("timed out waiting for browser login")
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	tok, err := postToken(ctx, md.TokenEndpoint, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {ClientID},
		"code_verifier": {verifier},
	})
	if err != nil {
		return nil, fmt.Errorf("exchanging authorization code: %w", err)
	}
	return credentialsFromToken(apiURL, tok), nil
}

func credentialsFromToken(apiURL string, tok *tokenResponse) *Credentials {
	return &Credentials{
		APIURL:       strings.TrimRight(apiURL, "/"),
		AccessToken:  tok.AccessToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second),
	}
}

// EnsureFresh refreshes the access token if it is expired or about to expire.
// The server rotates refresh tokens (120s grace), so refreshed credentials
// must be saved by the caller.
func EnsureFresh(ctx context.Context, c *Credentials) (creds *Credentials, refreshed bool, err error) {
	if c.EffectiveKind() != KindOAuth {
		return c, false, nil
	}
	if time.Until(c.ExpiresAt) > 30*time.Second {
		return c, false, nil
	}
	md, err := Discover(ctx, c.APIURL)
	if err != nil {
		return nil, false, err
	}
	tok, err := postToken(ctx, md.TokenEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {c.RefreshToken},
		"client_id":     {ClientID},
	})
	if err != nil {
		return nil, false, fmt.Errorf("refreshing session (run `flagsmith login` to re-authenticate): %w", err)
	}
	return credentialsFromToken(c.APIURL, tok), true, nil
}

// Revoke invalidates the refresh token (and its access tokens) server-side.
func Revoke(ctx context.Context, c *Credentials) error {
	md, err := Discover(ctx, c.APIURL)
	if err != nil {
		return err
	}
	if md.RevocationEndpoint == "" {
		return nil
	}
	form := url.Values{
		"token":           {c.RefreshToken},
		"token_type_hint": {"refresh_token"},
		"client_id":       {ClientID},
	}
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, md.RevocationEndpoint, strings.NewReader(form.Encode()),
	)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("revocation endpoint returned %s", resp.Status)
	}
	return nil
}

func resultPage(title, body string) string {
	return fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><title>%[1]s</title>
<style>body{font-family:-apple-system,system-ui,sans-serif;display:grid;place-items:center;min-height:90vh}main{text-align:center}</style>
</head><body><main><h1>%[1]s</h1><p>%[2]s</p></main></body></html>`, title, body)
}
