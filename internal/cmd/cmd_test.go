package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zalando/go-keyring"

	"github.com/Flagsmith/flagsmith-cli/internal/auth"
)

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

func run(args ...string) (string, error) {
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return buf.String(), err
}

// TestLoginStatusTokenLogoutFlow drives the full command surface against a
// fake Flagsmith instance, with the test playing the browser.
func TestLoginStatusTokenLogoutFlow(t *testing.T) {
	// Given
	keyring.MockInit()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	t.Setenv("AppData", tmp)

	var (
		mu      sync.Mutex
		revoked []url.Values
	)
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": srv.URL + "/oauth/authorize/",
			"token_endpoint":         srv.URL + "/o/token/",
			"revocation_endpoint":    srv.URL + "/o/revoke_token/",
		})
	})
	mux.HandleFunc("POST /o/token/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "cmd-access",
			"refresh_token": "cmd-refresh",
			"expires_in":    900,
			"scope":         auth.Scope,
			"token_type":    "Bearer",
		})
	})
	mux.HandleFunc("POST /o/revoke_token/", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		mu.Lock()
		revoked = append(revoked, r.PostForm)
		mu.Unlock()
	})
	mux.HandleFunc("GET /api/v1/auth/users/me/", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer cmd-access" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"email": "kim@example.com", "uuid": "u-1"})
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	// When
	out := &syncBuffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(out)
	rootCmd.SetArgs([]string{"login", "--api", srv.URL, "--no-browser"})
	done := make(chan error, 1)
	go func() { done <- rootCmd.Execute() }()

	authURLPattern := regexp.MustCompile(`https?://\S+/oauth/authorize/\?\S+`)
	var q url.Values
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && q == nil {
		if m := authURLPattern.FindString(out.String()); m != "" {
			u, err := url.Parse(m)
			if err != nil {
				t.Fatal(err)
			}
			q = u.Query()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if q == nil {
		t.Fatalf("login never printed an authorization URL; output: %q", out.String())
	}
	if _, err := http.Get(q.Get("redirect_uri") + "?code=c&state=" + url.QueryEscape(q.Get("state"))); err != nil {
		t.Fatal(err)
	}

	// Then
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("login: %v\noutput: %s", err, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("login did not return")
	}
	if got := out.String(); !strings.Contains(got, "✓ Logged in to "+srv.URL+" as kim@example.com") ||
		!strings.Contains(got, "keychain") {
		t.Errorf("login output = %q", got)
	}

	// When
	statusOut, err := run("auth", "status")

	// Then
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if !strings.Contains(statusOut, "kim@example.com") || !strings.Contains(statusOut, "keychain") {
		t.Errorf("auth status output = %q", statusOut)
	}

	// When
	tokenOut, err := run("auth", "token")

	// Then
	if err != nil {
		t.Fatalf("auth token: %v", err)
	}
	if strings.TrimSpace(tokenOut) != "cmd-access" {
		t.Errorf("auth token output = %q, want the access token", tokenOut)
	}

	// When
	logoutOut, err := run("logout")

	// Then
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(logoutOut, "Logged out of "+srv.URL) {
		t.Errorf("logout output = %q", logoutOut)
	}
	mu.Lock()
	if len(revoked) != 1 || revoked[0].Get("token") != "cmd-refresh" {
		t.Errorf("revocation calls = %+v, want one for cmd-refresh", revoked)
	}
	mu.Unlock()

	// When / Then
	if _, err := run("auth", "status"); !errors.Is(err, auth.ErrNotLoggedIn) {
		t.Errorf("auth status after logout = %v, want ErrNotLoggedIn", err)
	}

	// When / Then
	logoutOut, err = run("logout")
	if err != nil || !strings.Contains(logoutOut, "Not logged in") {
		t.Errorf("second logout = (%q, %v), want a friendly no-op", logoutOut, err)
	}
}
