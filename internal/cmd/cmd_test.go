package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
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

	"github.com/Flagsmith/flagsmith-cli/v2/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/version"
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
	// StringArray flags do not reset cleanly via Set(DefValue) — pflag appends
	// the "[]" default as a literal element — so clear them explicitly.
	apiHeaderFlags = nil
	apiFieldFlags = nil
	apiRawFields = nil
	evalTraitFlags = nil
}

// setEnvCred exports a credential variable host-scoped to url, as a
// self-hosted user must: the unscoped variable is trusted only for the
// default SaaS host.

// setEnvCred exports a credential variable host-scoped to url, as a
// self-hosted user must: the unscoped variable is trusted only for the
// default SaaS host.
func setEnvCred(t *testing.T, base, url, value string) {
	t.Helper()
	t.Setenv(scopedEnvName(base, url), value)
}

func setMasterKey(t *testing.T, url string) {
	t.Helper()
	setEnvCred(t, envAPIKey, url, masterKey)
}

func run(stdin string, args ...string) (string, error) {
	return runWithStdin(strings.NewReader(stdin), args...)
}

func runWithStdin(stdin io.Reader, args ...string) (string, error) {
	resetFlags()
	if args == nil {
		args = []string{} // nil would make cobra fall back to os.Args
	}
	buf := &bytes.Buffer{}
	rootCmd.SetOut(buf)
	rootCmd.SetErr(buf)
	rootCmd.SetIn(stdin)
	rootCmd.SetArgs(args)
	prepare()
	cmd, err := rootCmd.ExecuteC()
	if err != nil {
		reportError(cmd, err) // append hint + usage to buf, mirroring Execute
	}
	return buf.String(), err
}

// delayedReader stalls the first Read — a human thinking at a prompt.

// delayedReader stalls the first Read — a human thinking at a prompt.
type delayedReader struct {
	delay time.Duration
	r     io.Reader
	once  sync.Once
}

func (d *delayedReader) Read(p []byte) (int, error) {
	d.once.Do(func() { time.Sleep(d.delay) })
	return d.r.Read(p)
}

// fakeInstance is a Flagsmith instance stub covering the endpoints the auth
// slice touches. Organisations answers to the master key, the env bearer
// token, and the OAuth access token; users/me only to bearer credentials.

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

func fakeTTY(t *testing.T) {
	t.Helper()
	orig := stdinIsTTY
	stdinIsTTY = func() bool { return true }
	t.Cleanup(func() { stdinIsTTY = orig })
}

func TestSchemaURL(t *testing.T) {
	orig := version.Version
	t.Cleanup(func() { version.Version = orig })

	// Given
	version.Version = "v1.2.3"
	if got := schemaURL(); !strings.Contains(got, "/v1.2.3/schema/flagsmith.json") {
		t.Errorf("schemaURL() = %q, want pinned to the release tag", got)
	}

	// Given
	version.Version = "dev (2f71d6f)"
	if got := schemaURL(); !strings.Contains(got, "/main/schema/flagsmith.json") {
		t.Errorf("schemaURL() = %q, want main for a non-release build", got)
	}
}

func TestPromptSelfGuardsWithoutTTY(t *testing.T) {
	// Given
	orig := stdinIsTTY
	stdinIsTTY = func() bool { return false }
	t.Cleanup(func() { stdinIsTTY = orig })
	yesFlag = false
	initPrompts(rootCmd)

	// When / Then
	if _, err := selectPrompt(rootCmd, "project", "Project", []string{"a"}, 0); err == nil {
		t.Error("selectPrompt should refuse without a TTY")
	} else {
		var ue *usageError
		if !errors.As(err, &ue) || !strings.Contains(err.Error(), "--project") {
			t.Errorf("err = %v, want usage error naming --project", err)
		}
	}
}

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

func TestLogoutRevokeWarningGoesToStderr(t *testing.T) {
	// Given
	isolateStorage(t)
	if err := auth.Save(&auth.Credentials{
		Kind: auth.KindOAuth, APIURL: "http://127.0.0.1:1",
		AccessToken: "x", RefreshToken: "y",
		ExpiresAt: time.Now().Add(10 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}

	// When
	stdout, stderr, err := runSplit("", "logout", "--api-url", "http://127.0.0.1:1")

	// Then
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
	// Given
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

	// When
	_, err := run("", "login", "--api-url", f.srv.URL, "--no-input")

	// Then
	if err == nil || !strings.Contains(hintFor(err), "FLAGSMITH_API_KEY") {
		t.Errorf("err = %v (hint %q), want a refusal hinting at FLAGSMITH_API_KEY", err, hintFor(err))
	}
}

// --yes is authorization, not a liveness switch, so it must NOT block a
// browser login the way --no-input does: with --yes the login proceeds to the
// OAuth callback flow rather than refusing up front.

// --yes is authorization, not a liveness switch, so it must NOT block a
// browser login the way --no-input does: with --yes the login proceeds to the
// OAuth callback flow rather than refusing up front.
func TestBrowserLoginNotBlockedByYes(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)

	// When
	resetFlags()
	out := &syncBuffer{}
	rootCmd.SetOut(out)
	rootCmd.SetErr(out)
	rootCmd.SetIn(strings.NewReader(""))
	rootCmd.SetArgs([]string{"login", "--api-url", f.srv.URL, "--yes"})
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
		t.Fatalf("--yes must not refuse login; no authorization URL printed; output: %q", out.String())
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

func TestResolveCredentialRefreshesOnceUnderConcurrency(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	if err := auth.Save(&auth.Credentials{
		APIURL:       f.srv.URL,
		AccessToken:  "stale",
		RefreshToken: "cmd-refresh",
		ExpiresAt:    time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	apiURL = f.srv.URL
	resetCredentialCache()

	// When
	const n = 20
	var wg sync.WaitGroup
	results := make([]*activeCredential, n)
	errs := make([]error, n)
	start := make(chan struct{})
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = resolveCredential(context.Background())
		}(i)
	}
	close(start)
	wg.Wait()

	// Then
	if got := f.refreshCount(); got != 1 {
		t.Errorf("refresh POSTs = %d, want exactly 1 (herd collapsed)", got)
	}
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if results[i].token != oauthAccess {
			t.Errorf("caller %d token = %q, want the refreshed access token", i, results[i].token)
		}
	}
}

func TestFlagListSegmentFansOut(t *testing.T) {
	// Given
	f := flagUpdateEnv(t)
	override := map[string]any{"enabled": true, "feature_state_value": "x"}
	f.features["101"] = []map[string]any{
		{"id": 1, "name": "alpha", "type": "STANDARD", "segment_feature_state": override,
			"environment_feature_state": map[string]any{"enabled": true, "feature_state_value": nil}},
		{"id": 2, "name": "bravo", "type": "STANDARD", "segment_feature_state": override,
			"environment_feature_state": map[string]any{"enabled": true, "feature_state_value": nil}},
		{"id": 3, "name": "charlie", "type": "STANDARD", "segment_feature_state": override,
			"environment_feature_state": map[string]any{"enabled": true, "feature_state_value": nil}},
	}
	for id := 1; id <= 3; id++ {
		withFeatureSegments(f, id, map[string]any{
			"id": 1200 + id, "segment": 12, "segment_name": "powerusers", "priority": id - 1, "environment": 1,
		})
	}
	withFeatureSegmentDelay(f, 30*time.Millisecond)

	// When
	out, err := run("", "flag", "list", "--segment", "12")

	// Then
	if err != nil {
		t.Fatalf("flag list --segment: %v\noutput: %s", err, out)
	}
	alpha, bravo, charlie := strings.Index(out, "alpha"), strings.Index(out, "bravo"), strings.Index(out, "charlie")
	if alpha == -1 || bravo == -1 || charlie == -1 || alpha > bravo || bravo > charlie {
		t.Errorf("output = %q, want alpha, bravo, charlie in order", out)
	}
	if got := f.featureSegmentsCalls(); got != 3 {
		t.Errorf("feature-segments calls = %d, want 3", got)
	}
	if got := f.featureSegmentsPeak(); got < 2 {
		t.Errorf("feature-segments peak concurrency = %d, want the reads fanned out (>= 2)", got)
	}
}

func TestProject(t *testing.T) {
	t.Run("time at a confirmation does not count against the deadline", func(t *testing.T) {
		// Given
		f := flagUpdateEnv(t)
		_ = f
		fakeTTY(t)
		t.Setenv("FLAGSMITH_TIMEOUT", "1")
		stdin := &delayedReader{delay: 1100 * time.Millisecond, r: strings.NewReader("y\n")}

		// When
		out, err := runWithStdin(stdin, "project", "delete", "101")

		// Then
		if err != nil {
			t.Fatalf("project delete after a slow confirm: %v\noutput: %s", err, out)
		}
		if !strings.Contains(out, "Deleted project 101") {
			t.Errorf("output = %q, want the delete to succeed", out)
		}
	})

}

func TestUnscopedCredentialNotSentToRedirectedHost(t *testing.T) {
	setup := func(t *testing.T) *fakeInstance {
		t.Helper()
		isolateStorage(t)
		f := newFakeInstance(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "WqXhZk8sVY3dGgTqZ9pJmN", "apiUrl": "`+f.srv.URL+`"}`)
		return f
	}

	t.Run("withheld from a host the config named", func(t *testing.T) {
		// Given
		f := setup(t)
		t.Setenv(envAPIKey, masterKey)

		// When
		_, err := run("", "flag", "list")

		// Then
		if !errors.Is(err, auth.ErrNotLoggedIn) {
			t.Errorf("err = %v, want ErrNotLoggedIn (credential withheld)", err)
		}
		if got := f.featuresCalls(); got != 0 {
			t.Errorf("features calls = %d, want 0 — no request should carry the key", got)
		}
	})

	t.Run("sent when scoped to that host", func(t *testing.T) {
		// Given
		f := setup(t)
		setMasterKey(t, f.srv.URL)

		// When
		out, err := run("", "flag", "list")

		// Then
		if err != nil {
			t.Fatalf("flag list: %v\noutput: %s", err, out)
		}
		if got := f.featuresCalls(); got != 1 {
			t.Errorf("features calls = %d, want 1", got)
		}
	})
}

// The bearer variable is withheld from a redirected host like the master key.

// The bearer variable is withheld from a redirected host like the master key.
func TestUnscopedAccessTokenNotSentToRedirectedHost(t *testing.T) {
	// Given
	isolateStorage(t)
	f := newFakeInstance(t)
	root := tempRepo(t)
	writeConfig(t, root, `{"project": 101, "apiUrl": "`+f.srv.URL+`"}`)
	t.Setenv(envAccessToken, bearerToken)

	// When
	_, err := run("", "auth", "status")

	// Then
	if !errors.Is(err, auth.ErrNotLoggedIn) {
		t.Errorf("err = %v, want ErrNotLoggedIn (bearer withheld)", err)
	}
	if got := f.organisationLists(); got != 0 {
		t.Errorf("organisation calls = %d, want 0 — no request should carry the bearer", got)
	}
}

// The SDK credential is scoped to the SDK surface: sdkApiUrl's host, which is
// where that key is sent — not the Admin host, which can differ.

// The SDK credential is scoped to the SDK surface: sdkApiUrl's host, which is
// where that key is sent — not the Admin host, which can differ.
func TestSDKKeyScopesToSDKSurface(t *testing.T) {
	// admin and SDK surfaces on deliberately different hosts
	setup := func(t *testing.T) (admin, sdk *fakeInstance) {
		t.Helper()
		isolateStorage(t)
		admin, sdk = newFakeInstance(t), newFakeInstance(t)
		root := tempRepo(t)
		writeConfig(t, root, `{"project": 101, "environment": "WqXhZk8sVY3dGgTqZ9pJmN", "apiUrl": "`+
			admin.srv.URL+`", "sdkApiUrl": "`+sdk.srv.URL+`"}`)
		return admin, sdk
	}
	sentKey := func(t *testing.T) string {
		t.Helper()
		out, err := run("", "api", "--sdk", "api/v1/echo/")
		if err != nil {
			t.Fatalf("api --sdk: %v\noutput: %s", err, out)
		}
		var e map[string]any
		if err := json.Unmarshal([]byte(out), &e); err != nil {
			t.Fatalf("parsing %q: %v", out, err)
		}
		key, _ := e["envkey"].(string)
		return key
	}

	t.Run("a key scoped to the SDK host is used", func(t *testing.T) {
		// Given
		_, sdk := setup(t)
		setEnvCred(t, envEnvironmentKey, sdk.srv.URL, "ser.sdkSurfaceSecret")

		// When / Then
		if got := sentKey(t); got != "ser.sdkSurfaceSecret" {
			t.Errorf("X-Environment-Key = %q, want the SDK-scoped key", got)
		}
	})

	t.Run("a key scoped to the admin host is not used here", func(t *testing.T) {
		// Given
		admin, _ := setup(t)
		setEnvCred(t, envEnvironmentKey, admin.srv.URL, "ser.wrongSurfaceSecret")

		// When / Then
		if got := sentKey(t); got == "ser.wrongSurfaceSecret" {
			t.Errorf("X-Environment-Key = %q — a key scoped to the Admin host must not reach the SDK surface", got)
		}
	})

	t.Run("an unscoped key is withheld from a non-default SDK host", func(t *testing.T) {
		// Given
		setup(t)
		t.Setenv(envEnvironmentKey, "ser.unscopedSecret")

		// When / Then
		if got := sentKey(t); got == "ser.unscopedSecret" {
			t.Errorf("X-Environment-Key = %q — an unscoped secret must not follow a redirected SDK host", got)
		}
	})
}

func TestLoginFailsClosedWithoutKeychain(t *testing.T) {
	// Given
	isolateStorage(t)
	keyring.MockInitWithError(errors.New("keychain locked"))
	f := newFakeInstance(t)

	// When
	out, err := run("", "login", "--api-url", f.srv.URL, "--no-browser")

	// Then
	if err == nil {
		t.Fatalf("expected fail-closed error, got success: %q", out)
	}
	if !strings.Contains(hintFor(err), "FLAGSMITH_API_KEY") {
		t.Errorf("err = %v (hint %q), want a hint pointing at FLAGSMITH_API_KEY", err, hintFor(err))
	}
	if strings.Contains(out, "oauth/authorize") {
		t.Errorf("output = %q — the OAuth flow started despite no keychain", out)
	}
}
