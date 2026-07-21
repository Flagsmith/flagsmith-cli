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

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/zalando/go-keyring"

	"github.com/Flagsmith/flagsmith-cli/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/internal/cache"
	"github.com/Flagsmith/flagsmith-cli/internal/config"
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
	// Reset local flags on every command too — cobra's --help is a local
	// flag and would otherwise stay sticky across Execute calls.
	var resetAll func(c *cobra.Command)
	resetAll = func(c *cobra.Command) {
		c.Flags().VisitAll(reset)
		for _, sub := range c.Commands() {
			resetAll(sub)
		}
	}
	resetAll(rootCmd)
	noBrowser = false
	loginToken = false
	loginTokenStdin = false
	insecureStorage = false
}

func run(stdin string, args ...string) (string, error) {
	resetFlags()
	if args == nil {
		args = []string{} // nil would make cobra fall back to os.Args
	}
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

	mu          sync.Mutex
	revoked     []url.Values
	orgs        []map[string]any
	projects    map[string][]map[string]any // orgID -> projects
	envs        map[string][]map[string]any // projectID -> environments
	created     []string
	createdEnvs []string
}

func newFakeInstance(t *testing.T) *fakeInstance {
	t.Helper()
	f := &fakeInstance{
		orgs: []map[string]any{{"id": 3, "name": "Acme"}},
		projects: map[string][]map[string]any{
			"3": {{"id": 101, "name": "acme-api"}},
		},
		envs: map[string][]map[string]any{
			"101": {
				{"id": 1, "name": "Development", "api_key": "WqXhZk8sVY3dGgTqZ9pJmN"},
				{"id": 2, "name": "Production", "api_key": "K2mVsGdXhZ8kQqZ9pJmNbJ"},
			},
			"12345": {
				{"id": 3, "name": "Development", "api_key": "WqXhZk8sVY3dGgTqZ9pJmN"},
			},
		},
	}
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
	authorized := func(r *http.Request) bool {
		a := r.Header.Get("Authorization")
		return a == "Api-Key "+masterKey || a == "Bearer "+oauthAccess || a == "Bearer "+bearerToken
	}
	mux.HandleFunc("GET /api/v1/organisations/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		orgs := f.orgs
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"count": len(orgs), "results": orgs})
	})
	mux.HandleFunc("GET /api/v1/projects/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		projects := f.projects[r.URL.Query().Get("organisation")]
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"count": len(projects), "results": projects})
	})
	mux.HandleFunc("POST /api/v1/projects/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			Name         string `json:"name"`
			Organisation int    `json:"organisation"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.created = append(f.created, body.Name)
		f.mu.Unlock()
		f.mu.Lock()
		if f.envs["999"] == nil {
			f.envs["999"] = []map[string]any{} // created projects start empty
		}
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": 999, "name": body.Name})
	})
	mux.HandleFunc("GET /api/v1/environments/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		envs, known := f.envs[r.URL.Query().Get("project")]
		f.mu.Unlock()
		if !known {
			w.WriteHeader(http.StatusForbidden) // no access to this project
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"count": len(envs), "results": envs})
	})
	mux.HandleFunc("POST /api/v1/environments/", func(w http.ResponseWriter, r *http.Request) {
		if !authorized(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			Name    string `json:"name"`
			Project int    `json:"project"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.createdEnvs = append(f.createdEnvs, body.Name)
		f.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": 42, "name": body.Name, "api_key": "createdEnvKey00000000"})
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

// fakeTTY makes prompts believe stdin is a terminal for one test.
func fakeTTY(t *testing.T) {
	t.Helper()
	orig := stdinIsTTY
	stdinIsTTY = func() bool { return true }
	t.Cleanup(func() { stdinIsTTY = orig })
}

func loadWritten(t *testing.T, dir string) *config.File {
	t.Helper()
	f, _, err := config.Load(filepath.Join(dir, "flagsmith.json"))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestInitNonInteractive(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When
	out, err := run("", "init", "--api-url", f.srv.URL,
		"--project", "12345", "--environment", "WqXhZk8sVY3dGgTqZ9pJmN", "--yes")

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"Verified access", "Wrote flagsmith.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to contain %q", out, want)
		}
	}
	written := loadWritten(t, root)
	if written.Project != 12345 || written.Environment != "WqXhZk8sVY3dGgTqZ9pJmN" {
		t.Errorf("written = %+v", written)
	}
	if written.APIURL != f.srv.URL {
		t.Errorf("apiUrl = %q, want the non-SaaS instance recorded", written.APIURL)
	}
	if !strings.Contains(written.Schema, "schema/flagsmith.json") {
		t.Errorf("$schema = %q", written.Schema)
	}
	if got := cache.Load(f.srv.URL); got.Environments["WqXhZk8sVY3dGgTqZ9pJmN"] != "Development" {
		t.Errorf("cache = %+v, want environment names seeded", got)
	}
}

func TestInitNonInteractiveRequiresProject(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When
	_, err := run("", "init", "--api-url", f.srv.URL, "--yes")

	// Then
	var ue *usageError
	if !errors.As(err, &ue) {
		t.Fatalf("err = %v, want a usage error (exit 2)", err)
	}
	if !strings.Contains(err.Error(), "--project") {
		t.Errorf("err = %v, want it to name --project", err)
	}
}

func TestInitNoCredentials(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	tempRepo(t)

	// When
	_, err := run("", "init", "--api-url", f.srv.URL, "--project", "12345", "--yes")

	// Then
	if err == nil {
		t.Fatal("expected an error with no credentials")
	}
	for _, want := range []string{"FLAGSMITH_API_KEY", "flagsmith login"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
}

func TestInitRefusesOverwriteWithoutYes(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	writeConfig(t, root, `{"project": 1}`)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When
	_, err := run("", "init", "--api-url", f.srv.URL, "--project", "12345")

	// Then
	if err == nil || !strings.Contains(err.Error(), "--yes") {
		t.Errorf("err = %v, want a refusal pointing at --yes", err)
	}
}

func TestInitInteractiveMultiOrg(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	f.orgs = []map[string]any{{"id": 3, "name": "Acme"}, {"id": 7, "name": "Beta"}}
	f.projects["7"] = []map[string]any{{"id": 202, "name": "beta-app"}}
	f.envs["202"] = []map[string]any{
		{"id": 9, "name": "Development", "api_key": "BetaDevKey00000000000"},
	}
	root := tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)
	fakeTTY(t)

	// When — pick org 2 (Beta), project 1 (beta-app), environment 1 (Development)
	out, err := run("2\n1\n1\n", "init", "--api-url", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	for _, want := range []string{"Organisation", "Project", "environment", "Wrote flagsmith.json"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to contain %q", out, want)
		}
	}
	written := loadWritten(t, root)
	if written.Project != 202 || written.Environment != "BetaDevKey00000000000" {
		t.Errorf("written = %+v", written)
	}
	if written.Organisation != 7 {
		t.Errorf("organisation = %d, want 7 recorded for a multi-org user", written.Organisation)
	}
}

func TestInitInteractiveCreateProject(t *testing.T) {
	// Given — single org, one existing project; option 2 is "create new"
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)
	fakeTTY(t)

	// When — choose create (option 2), accept default project name, then
	// accept the default environment name (Development) for the empty project
	out, err := run("2\n\n\n", "init", "--api-url", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Created project") {
		t.Errorf("output = %q, want a created-project line", out)
	}
	f.mu.Lock()
	created := append([]string{}, f.created...)
	createdEnvs := append([]string{}, f.createdEnvs...)
	f.mu.Unlock()
	if len(created) != 1 || created[0] != filepath.Base(root) {
		t.Errorf("created = %v, want the cwd name as the default project name", created)
	}
	if len(createdEnvs) != 1 || createdEnvs[0] != "Development" {
		t.Errorf("createdEnvs = %v, want a Development environment created", createdEnvs)
	}
	if written := loadWritten(t, root); written.Project != 999 || written.Environment != "createdEnvKey00000000" {
		t.Errorf("written = %+v", written)
	}
}

func TestInitEmptyProjectPromptsEnvironmentCreation(t *testing.T) {
	// Given — an existing accessible project with no environments
	isolateStorage(t)
	f := newFakeInstance(t)
	f.envs["101"] = []map[string]any{}
	root := tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)
	fakeTTY(t)

	// When — pick the existing (empty) project, accept the env-name default
	out, err := run("1\n\n", "init", "--api-url", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "environment") {
		t.Errorf("output = %q, want an environment-creation prompt", out)
	}
	f.mu.Lock()
	createdEnvs := append([]string{}, f.createdEnvs...)
	f.mu.Unlock()
	if len(createdEnvs) != 1 || createdEnvs[0] != "Development" {
		t.Errorf("createdEnvs = %v, want a Development environment created", createdEnvs)
	}
	if written := loadWritten(t, root); written.Environment != "createdEnvKey00000000" {
		t.Errorf("written = %+v", written)
	}
}

func TestInitEmptyProjectNonInteractiveSkipsEnvironment(t *testing.T) {
	// Given — non-interactive init of an empty project
	isolateStorage(t)
	f := newFakeInstance(t)
	f.envs["101"] = []map[string]any{}
	root := tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When — no TTY, no environment given: don't create anything silently
	out, err := run("", "init", "--api-url", f.srv.URL, "--project", "101", "--yes")

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	f.mu.Lock()
	createdEnvs := append([]string{}, f.createdEnvs...)
	f.mu.Unlock()
	if len(createdEnvs) != 0 {
		t.Errorf("createdEnvs = %v, want none created without a TTY", createdEnvs)
	}
	if written := loadWritten(t, root); written.Environment != "" {
		t.Errorf("written = %+v, want no environment", written)
	}
}

func TestInitPreservesExistingOrganisation(t *testing.T) {
	// Given — a single-org user re-initialising a file that records its org
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	writeConfig(t, root, `{"project": 12345, "organisation": 3, "environment": "WqXhZk8sVY3dGgTqZ9pJmN"}`)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)

	// When — non-interactive re-init that doesn't touch the organisation
	out, err := run("", "init", "--api-url", f.srv.URL, "--project", "12345", "--yes")

	// Then — the organisation must survive, not be dropped
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	if written := loadWritten(t, root); written.Organisation != 3 {
		t.Errorf("organisation = %d, want 3 preserved", written.Organisation)
	}
}

func TestInitReinitReoffersOrgPicker(t *testing.T) {
	// Given — a multi-org user re-initialising; current org is NOT first
	isolateStorage(t)
	f := newFakeInstance(t)
	f.orgs = []map[string]any{{"id": 7, "name": "Beta"}, {"id": 3, "name": "Acme"}}
	f.projects["7"] = []map[string]any{{"id": 202, "name": "beta-app"}}
	f.envs["202"] = []map[string]any{{"id": 9, "name": "Development", "api_key": "BetaDevKey00000000000"}}
	root := tempRepo(t)
	writeConfig(t, root, `{"project": 101, "organisation": 3, "environment": "WqXhZk8sVY3dGgTqZ9pJmN"}`)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)
	fakeTTY(t)

	// When — accept the org picker default, then project 1, environment 1
	out, err := run("\n1\n1\n", "init", "--api-url", f.srv.URL)

	// Then — the picker was offered, and its default (the current org) held
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "Organisation") {
		t.Errorf("output = %q, want the org picker re-offered on re-init", out)
	}
	if written := loadWritten(t, root); written.Organisation != 3 {
		t.Errorf("organisation = %d, want the pre-selected current org (3)", written.Organisation)
	}
}

func TestInitInvalidChoiceReprompts(t *testing.T) {
	// Given — single org; project prompt has 2 options (acme-api, create new)
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)
	fakeTTY(t)

	// When — an invalid choice, then project 1, then environment 1
	out, err := run("99\n1\n1\n", "init", "--api-url", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "between 1 and") {
		t.Errorf("output = %q, want a re-prompt instead of a crash", out)
	}
	if written := loadWritten(t, root); written.Project != 101 {
		t.Errorf("written = %+v", written)
	}
}

func TestInitReinitShowsDiffAndConfirms(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	writeConfig(t, root, `{"project": 12345, "environment": "WqXhZk8sVY3dGgTqZ9pJmN"}`)
	t.Setenv("FLAGSMITH_API_KEY", masterKey)
	fakeTTY(t)

	// When — pick project 1 (acme-api, 101), env 2 (Production), confirm
	out, err := run("1\n2\ny\n", "init", "--api-url", f.srv.URL)

	// Then
	if err != nil {
		t.Fatalf("init: %v\noutput: %s", err, out)
	}
	for _, want := range []string{`- "project": 12345`, `+ "project": 101`, "Write changes?"} {
		if !strings.Contains(out, want) {
			t.Errorf("output = %q, want it to contain %q", out, want)
		}
	}
	if written := loadWritten(t, root); written.Project != 101 || written.Environment != "K2mVsGdXhZ8kQqZ9pJmNbJ" {
		t.Errorf("written = %+v", written)
	}
}

func TestBareInvocationNudgesInit(t *testing.T) {
	// Given
	isolateStorage(t)
	tempRepo(t)

	// When
	out, err := run("")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "flagsmith init") {
		t.Errorf("output = %q, want a nudge towards flagsmith init", out)
	}
}

// runSplit is run with stdout and stderr captured separately.
func runSplit(stdin string, args ...string) (string, string, error) {
	resetFlags()
	if args == nil {
		args = []string{}
	}
	outBuf, errBuf := &bytes.Buffer{}, &bytes.Buffer{}
	rootCmd.SetOut(outBuf)
	rootCmd.SetErr(errBuf)
	rootCmd.SetIn(strings.NewReader(stdin))
	rootCmd.SetArgs(args)
	err := rootCmd.Execute()
	return outBuf.String(), errBuf.String(), err
}

func TestJSONIsGlobal(t *testing.T) {
	t.Run("auth status --json", func(t *testing.T) {
		// Given
		isolateStorage(t)
		f := newFakeInstance(t)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		out, err := run("", "auth", "status", "--api-url", f.srv.URL, "--json")

		// Then
		if err != nil {
			t.Fatalf("auth status --json: %v", err)
		}
		var status struct {
			APIURL        string `json:"apiUrl"`
			Kind          string `json:"kind"`
			Organisations []struct {
				ID   int    `json:"id"`
				Name string `json:"name"`
			} `json:"organisations"`
			Source string `json:"credentialSource"`
		}
		if err := json.Unmarshal([]byte(out), &status); err != nil {
			t.Fatalf("parsing %q: %v", out, err)
		}
		if status.Kind != "master" || status.Source != "$FLAGSMITH_API_KEY" ||
			len(status.Organisations) != 1 || status.Organisations[0].Name != "Acme" {
			t.Errorf("status = %+v", status)
		}
	})

	t.Run("auth token --json", func(t *testing.T) {
		// Given
		isolateStorage(t)
		newFakeInstance(t)
		t.Setenv("FLAGSMITH_API_KEY", masterKey)

		// When
		out, err := run("", "auth", "token", "--json")

		// Then
		if err != nil {
			t.Fatalf("auth token --json: %v", err)
		}
		var token struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal([]byte(out), &token); err != nil {
			t.Fatalf("parsing %q: %v", out, err)
		}
		if token.Token != masterKey {
			t.Errorf("token = %q", token.Token)
		}
	})

	t.Run("FLAGSMITH_JSON_OUTPUT enables JSON", func(t *testing.T) {
		// Given
		isolateStorage(t)
		tempRepo(t)
		t.Setenv("FLAGSMITH_JSON_OUTPUT", "1")

		// When
		out, err := run("", "config")

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if !json.Valid([]byte(out)) {
			t.Errorf("output = %q, want JSON via env var", out)
		}
	})
}

func TestWarningsGoToStderr(t *testing.T) {
	// Given
	isolateStorage(t)
	root := tempRepo(t)
	writeConfig(t, root, `{"project": 1, "enviroment": "typo"}`)

	// When
	stdout, stderr, err := runSplit("", "config")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stdout, "unknown field") {
		t.Errorf("stdout = %q — warnings belong on stderr", stdout)
	}
	if !strings.Contains(stderr, "unknown field") {
		t.Errorf("stderr = %q, want the unknown-field warning", stderr)
	}
}

func TestLogoutRevokeWarningGoesToStderr(t *testing.T) {
	// Given — a stored OAuth session whose instance is unreachable
	isolateStorage(t)
	if err := auth.Save(&auth.Credentials{
		Kind: auth.KindOAuth, APIURL: "http://127.0.0.1:1",
		AccessToken: "x", RefreshToken: "y",
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}, auth.SourceKeychain); err != nil {
		t.Fatal(err)
	}

	// When
	stdout, stderr, err := runSplit("", "logout", "--api-url", "http://127.0.0.1:1")

	// Then — logout is a mutation: confirmation and warning on stderr,
	// stdout empty (no data result).
	if err != nil {
		t.Fatalf("logout: %v", err)
	}
	if stdout != "" {
		t.Errorf("stdout = %q, want empty", stdout)
	}
	if !strings.Contains(stderr, "Logged out") {
		t.Errorf("stderr = %q, want the confirmation", stderr)
	}
	if !strings.Contains(stderr, "Warning") {
		t.Errorf("stderr = %q, want the revoke warning", stderr)
	}
}

func TestBrowserLoginWithoutTTYNeverOpensBrowser(t *testing.T) {
	// Given — no TTY and no --no-browser flag; opening a real browser here
	// would violate 02 (and hijack the test machine)
	isolateStorage(t)
	f := newFakeInstance(t)

	// When
	resetFlags()
	out := &syncBuffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(out)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"login", "--api-url", f.srv.URL})
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
		t.Fatalf("no authorization URL printed; output: %q", out.String())
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
}

func TestBrowserLoginRefusesNoInput(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)

	// When — --yes promises zero interaction; waiting on a browser is interaction
	_, err := run("", "login", "--api-url", f.srv.URL, "--yes")

	// Then
	if err == nil || !strings.Contains(err.Error(), "--token-stdin") {
		t.Errorf("err = %v, want a refusal pointing at the non-interactive options", err)
	}
}

func TestInvalidEnvProjectIsUsageError(t *testing.T) {
	// Given
	isolateStorage(t)
	tempRepo(t)
	t.Setenv("FLAGSMITH_PROJECT", "not-a-number")

	// When
	_, err := run("", "config")

	// Then
	var usage *usageError
	if !errors.As(err, &usage) {
		t.Errorf("err = %v, want a usage error (exit 2) for invalid promptable input", err)
	}
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
