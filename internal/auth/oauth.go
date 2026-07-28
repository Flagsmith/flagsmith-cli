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

	"github.com/Flagsmith/flagsmith-cli/internal/bug"
)

const (
	// ClientID is the first-party public OAuth client pre-registered on every
	// Flagsmith instance.
	ClientID = "flagsmith-cli"

	Scope = "admin-api"

	loginTimeout = 5 * time.Minute

	// shutdownTimeout bounds the loopback server's teardown, so a client still
	// holding a connection cannot outlive the login it belongs to.
	shutdownTimeout = 5 * time.Second

	// readHeaderTimeout bounds how long a client may take to send its request
	// headers. The only legitimate one is a browser on this machine, so this is
	// generous; it stops a connection being held open indefinitely instead.
	readHeaderTimeout = 5 * time.Second
)

// Metadata is a subset of RFC 8414 authorization server metadata.
type Metadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RevocationEndpoint    string `json:"revocation_endpoint"`
}

// validate anchors the metadata to the instance it was fetched for, per RFC
// 8414 §3.3: metadata is served by one unauthenticated path, so without the
// issuer anchor a compromised /.well-known/* route could redirect the refresh
// token — the long-lived credential — to any host at the next quiet renewal.
// Endpoints that receive credentials (token, revocation) must live on the issuer
// over https. The authorization endpoint is browser-visited and carries no CLI
// secret (Flagsmith serves it from the dashboard host), so it may be
// cross-origin but must still be https. Loopback hosts are exempt from https so
// local instances keep working.
func (md *Metadata) validate(apiURL string) error {
	if md.Issuer == "" {
		return fmt.Errorf("authorization server metadata carries no issuer — cannot verify it speaks for %s", apiURL)
	}
	issuer, err := url.Parse(md.Issuer)
	if err != nil {
		return fmt.Errorf("authorization server metadata carries an invalid issuer: %w", err)
	}
	want, err := url.Parse(apiURL)
	if err != nil {
		return err
	}
	if !sameURL(issuer, want) {
		return fmt.Errorf("authorization server metadata is for %s, not %s — use the instance's canonical API URL", md.Issuer, apiURL)
	}
	if err := validCredentialEndpoint("token", md.TokenEndpoint, issuer); err != nil {
		return err
	}
	if md.RevocationEndpoint != "" {
		if err := validCredentialEndpoint("revocation", md.RevocationEndpoint, issuer); err != nil {
			return err
		}
	}
	authz, err := url.Parse(md.AuthorizationEndpoint)
	if err != nil {
		return fmt.Errorf("authorization endpoint %q is invalid: %w", md.AuthorizationEndpoint, err)
	}
	if !secureScheme(authz) {
		return fmt.Errorf("authorization endpoint %s is not https", md.AuthorizationEndpoint)
	}
	return nil
}

// validCredentialEndpoint checks an endpoint the refresh token is sent to:
// same origin as the issuer, over https (loopback exempt).
func validCredentialEndpoint(name, endpoint string, issuer *url.URL) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("%s endpoint %q is invalid: %w", name, endpoint, err)
	}
	if !strings.EqualFold(u.Scheme, issuer.Scheme) || !strings.EqualFold(u.Host, issuer.Host) {
		return fmt.Errorf("%s endpoint %s is not on %s — refusing to send credentials elsewhere", name, endpoint, issuer)
	}
	if !secureScheme(u) {
		return fmt.Errorf("%s endpoint %s is not https", name, endpoint)
	}
	return nil
}

// sameURL compares scheme, host, and path, ignoring case and a trailing slash.
func sameURL(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Host, b.Host) &&
		strings.TrimRight(a.Path, "/") == strings.TrimRight(b.Path, "/")
}

// secureScheme reports whether an endpoint may receive OAuth traffic: https,
// or plain http strictly on a loopback host (local instances).
func secureScheme(u *url.URL) bool {
	if strings.EqualFold(u.Scheme, "https") {
		return true
	}
	if !strings.EqualFold(u.Scheme, "http") {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func Discover(ctx context.Context, httpClient *http.Client, apiURL string) (*Metadata, error) {
	u := strings.TrimRight(apiURL, "/") + "/.well-known/oauth-authorization-server"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, bug.Mark(fmt.Errorf("reaching %s: %w", u, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s — is this a Flagsmith API URL?", u, resp.Status)
	}
	var md Metadata
	if err := json.NewDecoder(resp.Body).Decode(&md); err != nil {
		return nil, bug.Mark(fmt.Errorf("decoding authorization server metadata: %w", err))
	}
	if md.AuthorizationEndpoint == "" || md.TokenEndpoint == "" {
		return nil, bug.Mark(errors.New("authorization server metadata is missing required endpoints"))
	}
	if err := md.validate(strings.TrimRight(apiURL, "/")); err != nil {
		return nil, err
	}
	return &md, nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

func postToken(ctx context.Context, httpClient *http.Client, endpoint string, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()),
	)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
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
				return nil, bug.Mark(fmt.Errorf("%s: %s", oauthErr.Error, oauthErr.Description))
			}
			return nil, bug.Mark(errors.New(oauthErr.Error))
		}
		return nil, bug.Mark(fmt.Errorf("token endpoint returned %s", resp.Status))
	}
	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, bug.Mark(fmt.Errorf("decoding token response: %w", err))
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
func Login(ctx context.Context, httpClient *http.Client, apiURL string, openBrowser func(string) error, out io.Writer) (*Credentials, error) {
	md, err := Discover(ctx, httpClient, apiURL)
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
		return nil, bug.Mark(fmt.Errorf("starting loopback listener: %w", err))
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
	// Only the first callback is ever read, so later ones — a refreshed success
	// tab, a prefetch, anything else that can reach the loopback port — are
	// dropped. Sending unconditionally would park their handlers forever and
	// with them the deferred Shutdown, hanging a login that already succeeded.
	deliver := func(c callback) {
		select {
		case results <- c:
		default:
		}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			deliver(callback{err: bug.Mark(errors.New("state mismatch in callback — possible interception, aborting"))})
			return
		}
		if errCode := q.Get("error"); errCode != "" {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, resultPage("Login failed", "You can close this tab and return to your terminal."))
			deliver(callback{err: bug.Mark(fmt.Errorf("authorization failed: %s", errCode))})
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, resultPage("Login complete", "You can close this tab and return to your terminal."))
		deliver(callback{code: q.Get("code")})
	})
	server := &http.Server{Handler: mux, ReadHeaderTimeout: readHeaderTimeout}
	go server.Serve(ln) //nolint:errcheck // returns ErrServerClosed on shutdown
	defer func() {
		stop, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		server.Shutdown(stop) //nolint:errcheck // best-effort teardown
	}()

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
		return nil, bug.Mark(errors.New("timed out waiting for browser login"))
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	tok, err := postToken(ctx, httpClient, md.TokenEndpoint, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {ClientID},
		"code_verifier": {verifier},
	})
	if err != nil {
		return nil, bug.Mark(fmt.Errorf("exchanging authorization code: %w", err))
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

// ErrRefreshFailed means an expired session's refresh-token exchange failed, so
// the stored session is unusable.
var ErrRefreshFailed = errors.New("refreshing session failed")

// EnsureFresh refreshes the access token if it is expired or about to expire.
// The server rotates refresh tokens (120s grace), so refreshed credentials
// must be saved by the caller.
func EnsureFresh(ctx context.Context, httpClient *http.Client, c *Credentials) (creds *Credentials, refreshed bool, err error) {
	if c.EffectiveKind() != KindOAuth {
		return c, false, nil
	}
	if time.Until(c.ExpiresAt) > 30*time.Second {
		return c, false, nil
	}
	md, err := Discover(ctx, httpClient, c.APIURL)
	if err != nil {
		return nil, false, err
	}
	tok, err := postToken(ctx, httpClient, md.TokenEndpoint, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {c.RefreshToken},
		"client_id":     {ClientID},
	})
	if err != nil {
		return nil, false, fmt.Errorf("%w: %w", ErrRefreshFailed, err)
	}
	return credentialsFromToken(c.APIURL, tok), true, nil
}

// Revoke invalidates the refresh token (and its access tokens) server-side.
func Revoke(ctx context.Context, httpClient *http.Client, c *Credentials) error {
	md, err := Discover(ctx, httpClient, c.APIURL)
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
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return bug.Mark(fmt.Errorf("revocation endpoint returned %s", resp.Status))
	}
	return nil
}

func resultPage(title, body string) string {
	return fmt.Sprintf(`<!doctype html><html><head><meta charset="utf-8"><title>%[1]s</title>
<style>body{font-family:-apple-system,system-ui,sans-serif;display:grid;place-items:center;min-height:90vh}main{text-align:center}</style>
</head><body><main><h1>%[1]s</h1><p>%[2]s</p></main></body></html>`, title, body)
}
