package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/pflag"
	"github.com/zalando/go-keyring"

	"github.com/Flagsmith/flagsmith-cli/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/internal/cache"
)

const (
	masterKey   = "AbCd1234.0123456789abcdefABCDEF01234567"
	bearerToken = "envBearerToken0000000000000000"
	oauthAccess = "cmd-access"
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

func isolateStorage(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	t.Setenv("AppData", tmp)
}

// resetFlags clears package-level flag state that would otherwise leak
// between Execute calls on the shared rootCmd.
func resetFlags() {
	reset := func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	}
	rootCmd.PersistentFlags().VisitAll(reset)
	configCmd.Flags().VisitAll(reset)
	noBrowser = false
	loginToken = false
	loginTokenStdin = false
	insecureStorage = false
}

func run(stdin string, args ...string) (string, error) {
	resetFlags()
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return buf.String(), err
}

// fakeInstance is a Flagsmith instance stub covering the endpoints the auth
// slice touches. Organisations answers to the master key, the env bearer
// token, and the OAuth access token; users/me only to bearer credentials.
type fakeInstance struct {
	srv *httptest.Server

	mu      sync.Mutex
	revoked []url.Values
}

func newFakeInstance(t *testing.T) *fakeInstance {
	t.Helper()
	f := &fakeInstance{}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"authorization_endpoint": f.srv.URL + "/oauth/authorize/",
			"token_endpoint":         f.srv.URL + "/o/token/",
			"revocation_endpoint":    f.srv.URL + "/o/revoke_token/",
		})
	})
	mux.HandleFunc("POST /o/token/", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"access_token":  oauthAccess,
			"refresh_token": "cmd-refresh",
			"expires_in":    900,
			"scope":         auth.Scope,
			"token_type":    "Bearer",
		})
	})
	mux.HandleFunc("POST /o/revoke_token/", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		f.revoked = append(f.revoked, r.PostForm)
		f.mu.Unlock()
	})
	mux.HandleFunc("GET /api/v1/auth/users/me/", func(w http.ResponseWriter, r *http.Request) {
		a := r.Header.Get("Authorization")
		if a != "Bearer "+oauthAccess && a != "Bearer "+bearerToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"email": "kim@example.com", "uuid": "u-1"})
	})
	mux.HandleFunc("GET /api/v1/organisations/", func(w http.ResponseWriter, r *http.Request) {
		a := r.Header.Get("Authorization")
		if a != "Api-Key "+masterKey && a != "Bearer "+oauthAccess && a != "Bearer "+bearerToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"count":   1,
			"results": []map[string]any{{"id": 3, "name": "Acme"}},
		})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeInstance) revokeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.revoked)
}

// commandShapes are the two supported spellings of login/logout:
// top-level and under `auth`.
var commandShapes = []struct {
	name   string
	prefix []string
}{
	{"top-level", nil},
	{"auth alias", []string{"auth"}},
}

// shapeArgs prepends a shape prefix to a command line.
func shapeArgs(prefix []string, args ...string) []string {
	return append(append([]string{}, prefix...), args...)
}

func TestBrowserLoginFlow(t *testing.T) {
	for _, shape := range commandShapes {
		t.Run(shape.name, func(t *testing.T) {
			testBrowserLoginFlow(t, shape.prefix)
		})
	}
}

func testBrowserLoginFlow(t *testing.T, prefix []string) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)

	// When
	resetFlags()
	out := &syncBuffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(out)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs(shapeArgs(prefix, "login", "--api", f.srv.URL, "--no-browser"))
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
	if got := out.String(); !strings.Contains(got, "✓ Logged in to "+f.srv.URL+" as kim@example.com") ||
		!strings.Contains(got, "keychain") {
		t.Errorf("login output = %q", got)
	}

	// When
	statusOut, err := run("", "auth", "status", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	for _, want := range []string{"kim@example.com", "keychain", "Acme"} {
		if !strings.Contains(statusOut, want) {
			t.Errorf("auth status output = %q, want it to contain %q", statusOut, want)
		}
	}

	// When
	tokenOut, err := run("", "auth", "token", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("auth token: %v", err)
	}
	if strings.TrimSpace(tokenOut) != oauthAccess {
		t.Errorf("auth token output = %q, want the access token", tokenOut)
	}

	// When
	logoutOut, err := run("", shapeArgs(prefix, "logout", "--api", f.srv.URL)...)

	// Then
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(logoutOut, "Logged out of "+f.srv.URL) {
		t.Errorf("logout output = %q", logoutOut)
	}
	if f.revokeCount() != 1 {
		t.Errorf("revocations = %d, want 1", f.revokeCount())
	}

	// When / Then
	if _, err := run("", "auth", "status", "--api", f.srv.URL); !errors.Is(err, auth.ErrNotLoggedIn) {
		t.Errorf("auth status after logout = %v, want ErrNotLoggedIn", err)
	}

	// When / Then
	logoutOut, err = run("", shapeArgs(prefix, "logout", "--api", f.srv.URL)...)
	if err != nil || !strings.Contains(logoutOut, "Not logged in") {
		t.Errorf("second logout = (%q, %v), want a friendly no-op", logoutOut, err)
	}
}

func TestAPIFlagWorksInAnyPosition(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When / Then
	var outputs []string
	for _, args := range [][]string{
		{"auth", "status", "--api", f.srv.URL}, // after the subcommand (02 §8 shape)
		{"--api", f.srv.URL, "auth", "status"}, // before the subcommand
		{"auth", "--api", f.srv.URL, "status"}, // between nested subcommands
	} {
		out, err := run("", args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		outputs = append(outputs, out)
	}
	if outputs[0] != outputs[1] || outputs[1] != outputs[2] {
		t.Errorf("flag position changed behaviour:\n%q\n%q\n%q", outputs[0], outputs[1], outputs[2])
	}
	if !strings.Contains(outputs[0], "Acme") {
		t.Errorf("output = %q, want the fake instance to have been hit", outputs[0])
	}
}

func TestAPIURLFlagWithHiddenAlias(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When
	canonical, err := run("", "auth", "status", "--api-url", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("--api-url: %v", err)
	}
	if !strings.Contains(canonical, "Acme") {
		t.Errorf("output = %q, want the fake instance to have been hit", canonical)
	}

	// When
	alias, err := run("", "auth", "status", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("--api alias: %v", err)
	}
	if alias != canonical {
		t.Errorf("alias output differs from canonical:\n%q\n%q", alias, canonical)
	}

	// When / Then — the alias stays out of help
	helpOut, err := run("", "--help")
	if err != nil {
		t.Fatalf("--help: %v", err)
	}
	if !strings.Contains(helpOut, "--api-url") {
		t.Errorf("help = %q, want --api-url documented", helpOut)
	}
	if strings.Contains(helpOut, "--api ") {
		t.Errorf("help = %q, want the --api alias hidden", helpOut)
	}
}

// tempRepo creates a git repo dir, chdirs into it, and returns its path.
func tempRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return root
}

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "flagsmith.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func configJSON(t *testing.T, args ...string) map[string]map[string]any {
	t.Helper()
	out, err := run("", append([]string{"config", "--json"}, args...)...)
	if err != nil {
		t.Fatalf("config --json: %v\noutput: %s", err, out)
	}
	parsed := map[string]map[string]any{}
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("parsing %q: %v", out, err)
	}
	return parsed
}

func TestConfigCommand(t *testing.T) {
	t.Run("defaults with no config file", func(t *testing.T) {
		// Given
		isolateStorage(t)
		tempRepo(t)

		// When
		got := configJSON(t)

		// Then
		if v := got["apiUrl"]; v["value"] != "https://api.flagsmith.com" || v["source"] != "default" {
			t.Errorf("apiUrl = %v", v)
		}
		if v := got["sdkApiUrl"]; v["value"] != "https://edge.api.flagsmith.com" || v["source"] != "default" {
			t.Errorf("sdkApiUrl = %v", v)
		}
		if v := got["configPath"]; v["value"] != nil {
			t.Errorf("configPath = %v, want null", v)
		}
		if v := got["project"]; v["value"] != nil {
			t.Errorf("project = %v, want null", v)
		}
	})

	t.Run("file, env, cli sources with cache names", func(t *testing.T) {
		// Given
		isolateStorage(t)
		root := tempRepo(t)
		path := writeConfig(t, root, `{
			"project": 12345,
			"organisation": 3,
			"environment": "K2mVsGdXhZ8kQqZ9pJmNbJ",
			"apiUrl": "https://acme.example"
		}`)
		if err := cache.Merge("https://acme.example", &cache.Names{
			Organisations: map[string]string{"3": "Acme"},
			Projects:      map[string]string{"12345": "my-app"},
			Environments:  map[string]string{"StagingKey123": "Staging"},
		}); err != nil {
			t.Fatal(err)
		}
		t.Setenv("FLAGSMITH_ENVIRONMENT", "StagingKey123")

		// When
		got := configJSON(t, "--sdk-api-url", "https://flags.example")

		// Then
		if v := got["configPath"]; v["value"] != path || v["source"] != "default" {
			t.Errorf("configPath = %v", v)
		}
		if v := got["project"]; v["value"] != float64(12345) || v["name"] != "my-app" || v["source"] != "config" {
			t.Errorf("project = %v", v)
		}
		if v := got["organisation"]; v["value"] != float64(3) || v["name"] != "Acme" || v["source"] != "config" {
			t.Errorf("organisation = %v", v)
		}
		if v := got["environment"]; v["value"] != "StagingKey123" || v["name"] != "Staging" || v["source"] != "env" {
			t.Errorf("environment = %v", v)
		}
		if v := got["apiUrl"]; v["value"] != "https://acme.example" || v["source"] != "config" {
			t.Errorf("apiUrl = %v", v)
		}
		if v := got["sdkApiUrl"]; v["value"] != "https://flags.example" || v["source"] != "cli" {
			t.Errorf("sdkApiUrl = %v", v)
		}
	})

	t.Run("sdk api url follows a set api url", func(t *testing.T) {
		// Given
		isolateStorage(t)
		tempRepo(t)

		// When
		got := configJSON(t, "--api-url", "https://self.example")

		// Then
		if v := got["apiUrl"]; v["source"] != "cli" {
			t.Errorf("apiUrl = %v", v)
		}
		if v := got["sdkApiUrl"]; v["value"] != "https://self.example" || v["source"] != "default" {
			t.Errorf("sdkApiUrl = %v, want it to follow apiUrl as default", v)
		}
	})

	t.Run("explicit config path via flag", func(t *testing.T) {
		// Given
		isolateStorage(t)
		tempRepo(t)
		elsewhere := t.TempDir()
		path := writeConfig(t, elsewhere, `{"project": 7}`)

		// When
		got := configJSON(t, "--config-path", path)

		// Then
		if v := got["configPath"]; v["value"] != path || v["source"] != "cli" {
			t.Errorf("configPath = %v", v)
		}
		if v := got["project"]; v["value"] != float64(7) {
			t.Errorf("project = %v", v)
		}
	})

	t.Run("human output shows values and sources", func(t *testing.T) {
		// Given
		isolateStorage(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 12345}`)

		// When
		out, err := run("", "config")

		// Then
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"12345", "config", "https://api.flagsmith.com", "default", "flagsmith.json"} {
			if !strings.Contains(out, want) {
				t.Errorf("output = %q, want it to contain %q", out, want)
			}
		}
	})

	t.Run("unknown config fields warn on stderr", func(t *testing.T) {
		// Given
		isolateStorage(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 1, "enviroment": "typo"}`)

		// When
		out, err := run("", "config")

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "unknown field") {
			t.Errorf("output = %q, want an unknown-field warning", out)
		}
	})

	t.Run("bad FLAGSMITH_PROJECT errors", func(t *testing.T) {
		// Given
		isolateStorage(t)
		tempRepo(t)
		t.Setenv("FLAGSMITH_PROJECT", "not-a-number")

		// When / Then
		if _, err := run("", "config"); err == nil || !strings.Contains(err.Error(), "FLAGSMITH_PROJECT") {
			t.Errorf("err = %v, want a FLAGSMITH_PROJECT parse error", err)
		}
	})

	t.Run("server-side key via -e is rejected", func(t *testing.T) {
		// Given
		isolateStorage(t)
		tempRepo(t)

		// When / Then
		if _, err := run("", "config", "-e", "ser.AbCd"); err == nil ||
			!strings.Contains(err.Error(), "FLAGSMITH_ENVIRONMENT_KEY") {
			t.Errorf("err = %v, want a pointer to FLAGSMITH_ENVIRONMENT_KEY", err)
		}
	})
}

func TestAuthStatusHonoursConfigAPIURL(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	writeConfig(t, root, `{"project": 1, "apiUrl": "`+f.srv.URL+`"}`)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When — no --api-url flag anywhere
	out, err := run("", "auth", "status")

	// Then
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if !strings.Contains(out, "Acme") {
		t.Errorf("output = %q, want the config-file apiUrl to have been used", out)
	}
}

func TestAuthStatusSeedsNameCache(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When
	if _, err := run("", "auth", "status", "--api-url", f.srv.URL); err != nil {
		t.Fatal(err)
	}

	// Then
	if got := cache.Load(f.srv.URL); got.Organisations["3"] != "Acme" {
		t.Errorf("cache = %+v, want the organisation name remembered", got)
	}
}

func TestEnvMasterKey(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When
	statusOut, err := run("", "auth", "status", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	for _, want := range []string{"Master API key", "Acme", "$FLAGSMITH_API_KEY"} {
		if !strings.Contains(statusOut, want) {
			t.Errorf("auth status output = %q, want it to contain %q", statusOut, want)
		}
	}

	// When
	tokenOut, err := run("", "auth", "token", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("auth token: %v", err)
	}
	if strings.TrimSpace(tokenOut) != masterKey {
		t.Errorf("auth token output = %q, want the master key", tokenOut)
	}
}

func TestEnvBearerToken(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	t.Setenv("FLAGSMITH_API_KEY", bearerToken)

	// When
	statusOut, err := run("", "auth", "status", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	for _, want := range []string{"kim@example.com", "$FLAGSMITH_API_KEY"} {
		if !strings.Contains(statusOut, want) {
			t.Errorf("auth status output = %q, want it to contain %q", statusOut, want)
		}
	}
}

func TestEnvServerKeyRejected(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	t.Setenv("FLAGSMITH_API_KEY", "ser.AbCdEf1234")

	// When
	_, err := run("", "auth", "status", "--api", f.srv.URL)

	// Then
	if err == nil || !strings.Contains(err.Error(), "FLAGSMITH_ENVIRONMENT_KEY") {
		t.Errorf("err = %v, want mention of FLAGSMITH_ENVIRONMENT_KEY", err)
	}
}

func TestEnvBeatsKeychain(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	if err := auth.Save(&auth.Credentials{
		Kind: auth.KindOAuth, APIURL: f.srv.URL,
		AccessToken: oauthAccess, RefreshToken: "cmd-refresh",
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}, auth.SourceKeychain); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When
	statusOut, err := run("", "auth", "status", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if !strings.Contains(statusOut, "$FLAGSMITH_API_KEY") {
		t.Errorf("auth status output = %q, want the env source to win over the keychain", statusOut)
	}
}

func TestLoginTokenStdin(t *testing.T) {
	for _, shape := range commandShapes {
		t.Run(shape.name, func(t *testing.T) {
			testLoginTokenStdin(t, shape.prefix)
		})
	}
}

func testLoginTokenStdin(t *testing.T, prefix []string) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)

	// When
	loginOut, err := run(masterKey+"\n", shapeArgs(prefix, "login", "--api", f.srv.URL, "--token-stdin")...)

	// Then
	if err != nil {
		t.Fatalf("login --token-stdin: %v", err)
	}
	for _, want := range []string{"Master API key", "Acme", "keychain"} {
		if !strings.Contains(loginOut, want) {
			t.Errorf("login output = %q, want it to contain %q", loginOut, want)
		}
	}

	// When
	statusOut, err := run("", "auth", "status", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	for _, want := range []string{"Master API key", "Acme", "keychain"} {
		if !strings.Contains(statusOut, want) {
			t.Errorf("auth status output = %q, want it to contain %q", statusOut, want)
		}
	}

	// When
	logoutOut, err := run("", shapeArgs(prefix, "logout", "--api", f.srv.URL)...)

	// Then
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if !strings.Contains(logoutOut, "Logged out of "+f.srv.URL) {
		t.Errorf("logout output = %q", logoutOut)
	}
	if f.revokeCount() != 0 {
		t.Errorf("revocations = %d, want 0 — master keys have nothing to revoke", f.revokeCount())
	}
	if _, err := run("", "auth", "status", "--api", f.srv.URL); !errors.Is(err, auth.ErrNotLoggedIn) {
		t.Errorf("auth status after logout = %v, want ErrNotLoggedIn", err)
	}
}

func TestLoginTokenStdinRejectsServerKey(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)

	// When
	_, err := run("ser.AbCdEf1234\n", "login", "--api", f.srv.URL, "--token-stdin")

	// Then
	if err == nil || !strings.Contains(err.Error(), "FLAGSMITH_ENVIRONMENT_KEY") {
		t.Errorf("err = %v, want mention of FLAGSMITH_ENVIRONMENT_KEY", err)
	}
}

func TestLoginTokenStdinRejectsNonMasterKey(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)

	// When
	_, err := run(bearerToken+"\n", "login", "--api", f.srv.URL, "--token-stdin")

	// Then
	if err == nil || !strings.Contains(err.Error(), "Master API key") {
		t.Errorf("err = %v, want a not-a-master-key error", err)
	}
}

func TestLoginFailsClosedWithoutKeychain(t *testing.T) {
	// Given
	isolateStorage(t)
	keyring.MockInitWithError(errors.New("keychain locked"))
	f := newFakeInstance(t)

	// When
	out, err := run(masterKey+"\n", "login", "--api", f.srv.URL, "--token-stdin")

	// Then
	if err == nil {
		t.Fatalf("expected fail-closed error, got success: %q", out)
	}
	for _, want := range []string{"--insecure-storage", "FLAGSMITH_API_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
	if _, statErr := os.Stat(credentialsPath(t)); !os.IsNotExist(statErr) {
		t.Error("a credentials file was written without opt-in")
	}

	// When / Then — browser login probes the keychain before starting any flow
	out, err = run("", "login", "--api", f.srv.URL, "--no-browser")
	if err == nil || !strings.Contains(err.Error(), "--insecure-storage") {
		t.Errorf("browser login err = %v, want fail-closed before the flow", err)
	}
	if strings.Contains(out, "oauth/authorize") {
		t.Errorf("output = %q — the OAuth flow started despite no storage for its result", out)
	}
}

func credentialsPath(t *testing.T) string {
	t.Helper()
	path, err := auth.CredentialsFilePath()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestInsecureStorageOptIn(t *testing.T) {
	// Given
	isolateStorage(t)
	keyring.MockInitWithError(errors.New("keychain locked"))
	f := newFakeInstance(t)

	// When
	loginOut, err := run(masterKey+"\n", "login", "--api", f.srv.URL, "--token-stdin", "--insecure-storage")

	// Then
	if err != nil {
		t.Fatalf("login --insecure-storage: %v", err)
	}
	if !strings.Contains(loginOut, "plaintext") {
		t.Errorf("login output = %q, want a plaintext-storage warning", loginOut)
	}

	// When
	statusOut, err := run("", "auth", "status", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if !strings.Contains(statusOut, "file (plaintext)") {
		t.Errorf("auth status output = %q, want the file (plaintext) source", statusOut)
	}

	// When
	logoutOut, err := run("", "logout", "--api", f.srv.URL)

	// Then
	if err != nil || !strings.Contains(logoutOut, "Logged out") {
		t.Errorf("logout = (%q, %v)", logoutOut, err)
	}
}

func TestRefreshPersistsToSameStore(t *testing.T) {
	// Given a working keychain but credentials living in the opt-in file store
	isolateStorage(t)
	f := newFakeInstance(t)
	if err := auth.Save(&auth.Credentials{
		Kind: auth.KindOAuth, APIURL: f.srv.URL,
		AccessToken: "stale-access", RefreshToken: "cmd-refresh",
		ExpiresAt: time.Now().Add(-time.Minute),
	}, auth.SourceFile); err != nil {
		t.Fatal(err)
	}

	// When
	statusOut, err := run("", "auth", "status", "--api", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("auth status: %v", err)
	}
	if !strings.Contains(statusOut, "file (plaintext)") {
		t.Errorf("auth status output = %q, want the file (plaintext) source", statusOut)
	}
	if _, kerr := keyring.Get("flagsmith-cli", f.srv.URL); kerr == nil {
		t.Error("refresh migrated credentials into the keychain — must persist to the same store")
	}
	creds, source, err := auth.Load(f.srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if source != auth.SourceFile {
		t.Errorf("Load source = %q, want %q", source, auth.SourceFile)
	}
	if creds.AccessToken != oauthAccess {
		t.Errorf("AccessToken = %q, want the refreshed token persisted", creds.AccessToken)
	}
}
