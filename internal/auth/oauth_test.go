package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAuthServer implements just enough of the Flagsmith OAuth 2.1 surface
// (RFC 8414 metadata, token endpoint, revocation endpoint) to drive the flow.
type fakeAuthServer struct {
	srv *httptest.Server

	mu           sync.Mutex
	tokenForms   []url.Values
	revokeForms  []url.Values
	tokenHandler func(form url.Values) (status int, body any)
	noRevocation bool
}

func defaultTokenResponse(url.Values) (int, any) {
	return http.StatusOK, map[string]any{
		"access_token":  "access-1",
		"refresh_token": "refresh-1",
		"expires_in":    900,
		"scope":         Scope,
		"token_type":    "Bearer",
	}
}

func newFakeAuthServer(t *testing.T) *fakeAuthServer {
	t.Helper()
	f := &fakeAuthServer{tokenHandler: defaultTokenResponse}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		md := map[string]string{
			"authorization_endpoint": f.srv.URL + "/oauth/authorize/",
			"token_endpoint":         f.srv.URL + "/o/token/",
		}
		if !f.noRevocation {
			md["revocation_endpoint"] = f.srv.URL + "/o/revoke_token/"
		}
		json.NewEncoder(w).Encode(md)
	})
	mux.HandleFunc("POST /o/token/", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		f.tokenForms = append(f.tokenForms, r.PostForm)
		handler := f.tokenHandler
		f.mu.Unlock()
		status, body := handler(r.PostForm)
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	})
	mux.HandleFunc("POST /o/revoke_token/", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		f.revokeForms = append(f.revokeForms, r.PostForm)
		f.mu.Unlock()
	})

	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAuthServer) lastTokenForm(t *testing.T) url.Values {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.tokenForms) == 0 {
		t.Fatal("token endpoint was never called")
	}
	return f.tokenForms[len(f.tokenForms)-1]
}

// syncBuffer is a goroutine-safe io.Writer for capturing Login's output
// while the flow is still blocked waiting for the browser callback.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

var authURLPattern = regexp.MustCompile(`https?://\S+/oauth/authorize/\?\S+`)

// waitForAuthURL polls out until Login has printed the authorization URL,
// returning its query parameters and the redirect URI.
func waitForAuthURL(t *testing.T, out *syncBuffer) (url.Values, string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m := authURLPattern.FindString(out.String()); m != "" {
			u, err := url.Parse(m)
			if err != nil {
				t.Fatalf("parsing printed auth URL %q: %v", m, err)
			}
			q := u.Query()
			if q.Get("redirect_uri") == "" {
				t.Fatalf("auth URL %q has no redirect_uri", m)
			}
			return q, q.Get("redirect_uri")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("Login never printed an authorization URL; output so far: %q", out.String())
	return nil, ""
}

type loginResult struct {
	creds *Credentials
	err   error
}

func startLogin(t *testing.T, ctx context.Context, apiURL string, openBrowser func(string) error) (*syncBuffer, <-chan loginResult) {
	t.Helper()
	out := &syncBuffer{}
	results := make(chan loginResult, 1)
	go func() {
		creds, err := Login(ctx, apiURL, openBrowser, out)
		results <- loginResult{creds, err}
	}()
	return out, results
}

func awaitLogin(t *testing.T, results <-chan loginResult) loginResult {
	t.Helper()
	select {
	case r := <-results:
		return r
	case <-time.After(10 * time.Second):
		t.Fatal("Login did not return")
		return loginResult{}
	}
}

func TestLoginHappyPath(t *testing.T) {
	f := newFakeAuthServer(t)
	out, results := startLogin(t, context.Background(), f.srv.URL, nil)

	q, redirectURI := waitForAuthURL(t, out)

	// The authorization request must be exactly what the server contract needs.
	for param, want := range map[string]string{
		"client_id":             ClientID,
		"response_type":         "code",
		"scope":                 Scope,
		"code_challenge_method": "S256",
	} {
		if got := q.Get(param); got != want {
			t.Errorf("auth URL %s = %q, want %q", param, got, want)
		}
	}
	if q.Get("state") == "" || q.Get("code_challenge") == "" {
		t.Error("auth URL is missing state or code_challenge")
	}
	if !strings.HasPrefix(redirectURI, "http://127.0.0.1:") {
		t.Errorf("redirect_uri %q must use literal 127.0.0.1 (RFC 8252 loopback)", redirectURI)
	}

	// Play the browser: deliver the code to the loopback listener.
	resp, err := http.Get(redirectURI + "?code=test-code&state=" + url.QueryEscape(q.Get("state")))
	if err != nil {
		t.Fatalf("delivering callback: %v", err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(page), "Login complete") {
		t.Errorf("callback response = %d %q, want 200 with success page", resp.StatusCode, page)
	}

	r := awaitLogin(t, results)
	if r.err != nil {
		t.Fatalf("Login: %v", r.err)
	}
	if r.creds.AccessToken != "access-1" || r.creds.RefreshToken != "refresh-1" {
		t.Errorf("unexpected credentials: %+v", r.creds)
	}
	if r.creds.APIURL != f.srv.URL {
		t.Errorf("APIURL = %q, want %q", r.creds.APIURL, f.srv.URL)
	}
	if until := time.Until(r.creds.ExpiresAt); until < 13*time.Minute || until > 15*time.Minute {
		t.Errorf("ExpiresAt %v not ~15m out", until)
	}

	// The token exchange must round-trip the code, redirect URI and PKCE verifier.
	form := f.lastTokenForm(t)
	for param, want := range map[string]string{
		"grant_type":   "authorization_code",
		"code":         "test-code",
		"client_id":    ClientID,
		"redirect_uri": redirectURI,
	} {
		if got := form.Get(param); got != want {
			t.Errorf("token form %s = %q, want %q", param, got, want)
		}
	}
	sum := sha256.Sum256([]byte(form.Get("code_verifier")))
	if got := base64.RawURLEncoding.EncodeToString(sum[:]); got != q.Get("code_challenge") {
		t.Error("code_verifier does not hash to the code_challenge sent in the auth URL")
	}
}

func TestLoginOpensBrowserAndSurvivesBrowserError(t *testing.T) {
	f := newFakeAuthServer(t)
	opened := make(chan string, 1)
	openBrowser := func(u string) error {
		opened <- u
		return errors.New("no browser installed")
	}
	out, results := startLogin(t, context.Background(), f.srv.URL, openBrowser)

	q, redirectURI := waitForAuthURL(t, out)
	select {
	case u := <-opened:
		if !strings.Contains(u, "oauth/authorize/") {
			t.Errorf("browser opened with unexpected URL %q", u)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("openBrowser was never called")
	}

	http.Get(redirectURI + "?code=c&state=" + url.QueryEscape(q.Get("state")))
	if r := awaitLogin(t, results); r.err != nil {
		t.Fatalf("Login should survive a browser-open failure, got: %v", r.err)
	}
	if !strings.Contains(out.String(), "open the URL above manually") {
		t.Error("expected a manual-fallback hint after browser-open failure")
	}
}

func TestLoginRejectsStateMismatch(t *testing.T) {
	f := newFakeAuthServer(t)
	out, results := startLogin(t, context.Background(), f.srv.URL, nil)

	_, redirectURI := waitForAuthURL(t, out)
	resp, err := http.Get(redirectURI + "?code=stolen&state=attacker-guess")
	if err != nil {
		t.Fatalf("delivering callback: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("callback with bad state = %d, want 400", resp.StatusCode)
	}

	r := awaitLogin(t, results)
	if r.err == nil || !strings.Contains(r.err.Error(), "state mismatch") {
		t.Errorf("Login error = %v, want state mismatch", r.err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.tokenForms) != 0 {
		t.Error("token endpoint must not be called after a state mismatch")
	}
}

func TestLoginDenied(t *testing.T) {
	f := newFakeAuthServer(t)
	out, results := startLogin(t, context.Background(), f.srv.URL, nil)

	q, redirectURI := waitForAuthURL(t, out)
	http.Get(redirectURI + "?error=access_denied&state=" + url.QueryEscape(q.Get("state")))

	r := awaitLogin(t, results)
	if r.err == nil || !strings.Contains(r.err.Error(), "access_denied") {
		t.Errorf("Login error = %v, want access_denied", r.err)
	}
}

func TestLoginContextCanceled(t *testing.T) {
	f := newFakeAuthServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	out, results := startLogin(t, ctx, f.srv.URL, nil)

	waitForAuthURL(t, out) // ensure the flow is blocked on the callback
	cancel()

	r := awaitLogin(t, results)
	if !errors.Is(r.err, context.Canceled) {
		t.Errorf("Login error = %v, want context.Canceled", r.err)
	}
}

func TestLoginExchangeFailure(t *testing.T) {
	f := newFakeAuthServer(t)
	f.tokenHandler = func(url.Values) (int, any) {
		return http.StatusBadRequest, map[string]string{
			"error":             "invalid_grant",
			"error_description": "Code has expired.",
		}
	}
	out, results := startLogin(t, context.Background(), f.srv.URL, nil)

	q, redirectURI := waitForAuthURL(t, out)
	http.Get(redirectURI + "?code=late&state=" + url.QueryEscape(q.Get("state")))

	r := awaitLogin(t, results)
	if r.err == nil || !strings.Contains(r.err.Error(), "invalid_grant: Code has expired.") {
		t.Errorf("Login error = %v, want invalid_grant with description", r.err)
	}
}

func TestDiscover(t *testing.T) {
	t.Run("happy path", func(t *testing.T) {
		f := newFakeAuthServer(t)
		md, err := Discover(context.Background(), f.srv.URL+"/") // trailing slash must not break the path
		if err != nil {
			t.Fatal(err)
		}
		if md.TokenEndpoint != f.srv.URL+"/o/token/" {
			t.Errorf("TokenEndpoint = %q", md.TokenEndpoint)
		}
	})

	t.Run("not a flagsmith API", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		defer srv.Close()
		_, err := Discover(context.Background(), srv.URL)
		if err == nil || !strings.Contains(err.Error(), "is this a Flagsmith API URL?") {
			t.Errorf("err = %v, want a helpful not-Flagsmith hint", err)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "<html>nope</html>")
		}))
		defer srv.Close()
		if _, err := Discover(context.Background(), srv.URL); err == nil {
			t.Error("expected an error for non-JSON metadata")
		}
	})

	t.Run("missing endpoints", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "{}")
		}))
		defer srv.Close()
		if _, err := Discover(context.Background(), srv.URL); err == nil {
			t.Error("expected an error for metadata without endpoints")
		}
	})
}

func TestEnsureFresh(t *testing.T) {
	t.Run("valid token is untouched", func(t *testing.T) {
		c := &Credentials{APIURL: "http://unreachable.invalid", ExpiresAt: time.Now().Add(10 * time.Minute)}
		got, refreshed, err := EnsureFresh(context.Background(), c)
		if err != nil || refreshed || got != c {
			t.Errorf("got (%v, %v, %v), want the same credentials untouched", got, refreshed, err)
		}
	})

	t.Run("expired token is refreshed", func(t *testing.T) {
		f := newFakeAuthServer(t)
		c := &Credentials{APIURL: f.srv.URL, RefreshToken: "old-refresh", ExpiresAt: time.Now().Add(-time.Minute)}
		got, refreshed, err := EnsureFresh(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		if !refreshed || got.AccessToken != "access-1" || got.RefreshToken != "refresh-1" {
			t.Errorf("got (%+v, refreshed=%v)", got, refreshed)
		}
		form := f.lastTokenForm(t)
		for param, want := range map[string]string{
			"grant_type":    "refresh_token",
			"refresh_token": "old-refresh",
			"client_id":     ClientID,
		} {
			if got := form.Get(param); got != want {
				t.Errorf("refresh form %s = %q, want %q", param, got, want)
			}
		}
	})

	t.Run("revoked refresh token points at re-login", func(t *testing.T) {
		f := newFakeAuthServer(t)
		f.tokenHandler = func(url.Values) (int, any) {
			return http.StatusBadRequest, map[string]string{"error": "invalid_grant"}
		}
		c := &Credentials{APIURL: f.srv.URL, RefreshToken: "revoked", ExpiresAt: time.Now().Add(-time.Minute)}
		_, _, err := EnsureFresh(context.Background(), c)
		if err == nil || !strings.Contains(err.Error(), "flagsmith login") {
			t.Errorf("err = %v, want a hint to run `flagsmith login`", err)
		}
	})
}

func TestRevoke(t *testing.T) {
	t.Run("revokes the refresh token", func(t *testing.T) {
		f := newFakeAuthServer(t)
		c := &Credentials{APIURL: f.srv.URL, RefreshToken: "refresh-1"}
		if err := Revoke(context.Background(), c); err != nil {
			t.Fatal(err)
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if len(f.revokeForms) != 1 {
			t.Fatalf("revocation endpoint called %d times, want 1", len(f.revokeForms))
		}
		form := f.revokeForms[0]
		for param, want := range map[string]string{
			"token":           "refresh-1",
			"token_type_hint": "refresh_token",
			"client_id":       ClientID,
		} {
			if got := form.Get(param); got != want {
				t.Errorf("revoke form %s = %q, want %q", param, got, want)
			}
		}
	})

	t.Run("no revocation endpoint is a no-op", func(t *testing.T) {
		f := newFakeAuthServer(t)
		f.noRevocation = true
		c := &Credentials{APIURL: f.srv.URL, RefreshToken: "refresh-1"}
		if err := Revoke(context.Background(), c); err != nil {
			t.Errorf("Revoke without a revocation endpoint should succeed, got %v", err)
		}
	})
}
