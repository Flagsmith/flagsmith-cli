package cmd

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/cache"
	"github.com/rogpeppe/go-internal/testscript"
	"github.com/zalando/go-keyring"
)

// PROTOTYPE 2 — the same transcripts as testscript scripts.
//
// Each .txtar under testdata/script is one scenario: the files it starts with,
// the commands it runs, and the output it expects, in one file. The CLI runs as
// a real subprocess, so exit codes, os.Stderr and process-global state are the
// real thing rather than something the harness simulates.
//
//	go test ./internal/cmd -run TestScripts -update-scripts

var updateScripts = flag.Bool("update-scripts", false, "rewrite expected output in testdata/script")

// TestMain lets the test binary impersonate the CLI, so `flagsmith` inside a
// script is a real process running the real main. Main always calls os.Exit.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"flagsmith": func() {
			// Mocked here, inside the impersonated binary: this is a fresh
			// process, so without it the CLI would read and write the
			// developer's real OS keychain.
			keyring.MockInit()

			// The mock lives only as long as this process, so a script's
			// keychain travels between invocations as a file. Execute returns
			// on success and exits on failure, so what a failed invocation
			// wrote is not carried forward.
			loadScriptKeychain()
			Execute()
			saveScriptKeychain()
		},
	})
}

// $KEYCHAIN names the file a script's keychain lives in. A script seeds it as
// an ordinary txtar file, and reads it back to see what a command stored:
//
//	-- keychain.json --
//	[{"api_url": "$API", "kind": "oauth", "access_token": "stale"}]
const keychainEnv = "KEYCHAIN"

// scriptKeychainURLs are the instances whose credentials travel between
// invocations: the fake, plus whatever a script seeded. The keychain is keyed
// by instance URL and offers no way to enumerate, so the set is tracked here.
var scriptKeychainURLs []string

// loadScriptKeychain puts the credentials a script seeded into the mock.
func loadScriptKeychain() {
	if api := os.Getenv("API"); api != "" {
		scriptKeychainURLs = append(scriptKeychainURLs, api)
	}
	raw, err := os.ReadFile(os.Getenv(keychainEnv))
	if err != nil {
		return
	}
	// txtar file bodies are not environment-substituted, so a seed names the
	// fake as $API and it is expanded here.
	raw = []byte(strings.ReplaceAll(string(raw), "$API", os.Getenv("API")))
	var creds []auth.Credentials
	if err := json.Unmarshal(raw, &creds); err != nil {
		fmt.Fprintln(os.Stderr, "seeding the keychain:", err)
		os.Exit(1)
	}
	for _, c := range creds {
		if err := auth.Save(&c); err != nil {
			fmt.Fprintln(os.Stderr, "seeding the keychain:", err)
			os.Exit(1)
		}
		scriptKeychainURLs = append(scriptKeychainURLs, c.APIURL)
	}
}

// saveScriptKeychain writes back what the invocation left in the keychain, for
// the next invocation and for the script to assert on.
func saveScriptKeychain() {
	path := os.Getenv(keychainEnv)
	if path == "" {
		return
	}
	var creds []auth.Credentials
	seen := map[string]bool{}
	for _, url := range scriptKeychainURLs {
		if seen[url] {
			continue
		}
		seen[url] = true
		if c, err := auth.Load(url); err == nil && c != nil {
			creds = append(creds, *c)
		}
	}
	if len(creds) == 0 {
		_ = os.Remove(path)
		return
	}
	body, err := json.Marshal(creds)
	if err == nil {
		err = os.WriteFile(path, body, 0o600)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "saving the keychain:", err)
		os.Exit(1)
	}
}

func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir:           filepath.Join("testdata", "script"),
		UpdateScripts: *updateScripts,
		// Scripts name the binary explicitly — `exec flagsmith ...` — so a
		// typo'd command is an error rather than a silent shell lookup.
		RequireExplicitExec: true,
		Setup:               setupScript,
		Cmds: map[string]func(*testscript.TestScript, bool, []string){
			"fake":  cmdFake,
			"dump":  cmdDump,
			"cache": cmdCache,
			"subst": cmdSubst,
		},
	})
}

// backref spots a description that leans on a neighbouring case. Whole words
// only: "against" is not a back-reference to "again".
var backref = regexp.MustCompile(`\b(the same|above|again|previously|previous|earlier)\b`)

// Every case in a script is described the way the engine's test data describes
// its own: a Given/When/Then triple, in that order, above the commands it
// covers. The scan keeps it honest — a case added without one fails here.
func TestEveryScriptCaseIsGivenWhenThen(t *testing.T) {
	dir := filepath.Join("testdata", "script")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no scripts found — the scan is broken")
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			// Only the script itself, not the txtar file sections after it.
			script := string(body)
			if i := strings.Index(script, "\n-- "); i >= 0 {
				script = script[:i]
			}
			// A case is a block of lines separated by blank ones. Blocks that
			// run nothing — a bare `fake` setting, say — describe nothing.
			for _, block := range strings.Split(script, "\n\n") {
				lines := strings.Split(strings.TrimSpace(block), "\n")
				if !slices.ContainsFunc(lines, func(l string) bool {
					return strings.HasPrefix(strings.TrimPrefix(l, "! "), "exec ")
				}) {
					continue
				}
				var got []string
				for _, l := range lines {
					for _, want := range []string{"# Given:", "# When:", "# Then:"} {
						if strings.HasPrefix(l, want) {
							got = append(got, strings.TrimSuffix(want, ":"))
						}
					}
				}
				if want := []string{"# Given", "# When", "# Then"}; !slices.Equal(got, want) {
					t.Errorf("case has %v, want %v, in:\n%s", got, want, strings.TrimSpace(block))
				}
				// Each case must read on its own: a description that points at
				// a neighbour ("the same environment") is only legible to
				// someone reading the whole file top to bottom.
				for _, l := range lines {
					if !strings.HasPrefix(l, "# ") {
						continue
					}
					if backref.MatchString(strings.ToLower(l)) {
						t.Errorf("%q refers to another case — describe this one in full", strings.TrimSpace(l))
					}
				}
			}
		})
	}
}

// setupScript gives each script its own fake instance and points the CLI at it.
func setupScript(env *testscript.Env) error {
	f := newFake()
	env.Defer(f.srv.Close)
	env.Values["fake"] = f

	// Fail closed. Every script is pinned to the fake by the environment, not
	// by remembering to pass --api-url: a script that forgets cannot reach
	// api.flagsmith.com and quietly authenticate as the developer.
	env.Setenv("FLAGSMITH_API_URL", f.srv.URL)
	env.Setenv(scopedEnvName(envAPIKey, f.srv.URL), masterKey)

	// $API is for cmpenv, which substitutes it in expected output — the fake's
	// port changes every run and must not be baked into a golden file.
	env.Setenv("API", f.srv.URL)

	// Credential variables are host-scoped, so their names carry the fake's
	// port and no script can spell them literally. Export the names instead,
	// for a script that needs to set one:
	//
	//	env $SDK_KEY_VAR=ser.serverSideSecret
	env.Setenv("SDK_KEY_VAR", scopedEnvName(envEnvironmentKey, f.srv.URL))
	env.Setenv("API_KEY_VAR", scopedEnvName(envAPIKey, f.srv.URL))
	env.Setenv("TOKEN_VAR", scopedEnvName(envAccessToken, f.srv.URL))

	// An `env` in a script lasts to the end of that script, so a case that
	// clears the credential changes what its neighbours start from. $MASTER_KEY
	// lets a case put it back rather than depend on what ran before it.
	env.Setenv("MASTER_KEY", masterKey)
	env.Setenv("BEARER_TOKEN", bearerToken)
	env.Setenv("HOME", env.WorkDir)
	env.Setenv("XDG_CONFIG_HOME", filepath.Join(env.WorkDir, ".config"))
	// os.UserCacheDir and os.UserConfigDir read these on Windows.
	env.Setenv("LocalAppData", filepath.Join(env.WorkDir, "AppData", "Local"))
	env.Setenv("AppData", filepath.Join(env.WorkDir, "AppData", "Roaming"))

	// $CACHE is the name cache the CLI will use, so a script can read it
	// without spelling out a per-platform path.
	env.Setenv("CACHE", cachePathFor(env.WorkDir, ""))
	env.Setenv(keychainEnv, filepath.Join(env.WorkDir, "keychain.json"))
	return nil
}

// cmdFake configures the instance a script runs against, for the conditions
// that are properties of the backend rather than of the invocation.
//
//	fake sdk-status 500       the SDK endpoints answer 500
//	fake sdk-delay 1200ms     they take this long
//	fake sdk-flags <key>      that environment key resolves the default flags
//	fake sdk-flags <key> none ...and this one resolves none
func cmdFake(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) == 0 {
		ts.Fatalf("usage: fake <sdk-status|sdk-delay|sdk-flags|environments> [value...]")
	}
	valueless := []string{"environments", "orgs", "feature-overrides", "workflow-gated",
		"segment-override-missing", "edge-identities", "forget-requests", "features-default", "environments-named"}
	if len(args) < 2 && !slices.Contains(valueless, args[0]) {
		ts.Fatalf("fake %s needs a value", args[0])
	}
	// These name a target and a fixture file, so say which is missing rather
	// than indexing past the end of the arguments.
	withFixture := []string{"feature-segments", "feature-states", "project-environments",
		"server-keys", "projects"}
	if len(args) < 3 && slices.Contains(withFixture, args[0]) {
		ts.Fatalf("usage: fake %s <target> <file>", args[0])
	}
	f := ts.Value("fake").(*fakeInstance)
	// Not locked here: several of these delegate to fixture helpers that take
	// the lock themselves. Cases that touch fields directly lock around it.
	set := func(fn func()) { f.mu.Lock(); defer f.mu.Unlock(); fn() }
	switch args[0] {
	case "sdk-status":
		code, err := strconv.Atoi(args[1])
		ts.Check(err)
		set(func() { f.sdkStatus = code })
	case "sdk-delay":
		d, err := time.ParseDuration(args[1])
		ts.Check(err)
		set(func() { f.sdkDelay = d })
	case "sdk-flags":
		flags := sdkFlagsFrom(defaultFeatures())
		switch {
		case len(args) > 2 && args[2] == "none":
			flags = []map[string]any{}
		case len(args) > 2 && args[2] == "unknown":
			// Not in the map at all: the SDK API 401s, as it does for a key
			// that belongs to no environment.
			set(func() { delete(f.sdkEnvFlags, args[1]) })
			return
		}
		set(func() { f.sdkEnvFlags[args[1]] = flags })
	case "sdk-identity":
		// This identity resolves max_items on at 99, so its own flags are
		// distinguishable from the environment's defaults.
		flags := sdkFlagsFrom(defaultFeatures())
		flags[1]["enabled"], flags[1]["feature_state_value"] = true, 99
		set(func() { f.sdkIdentityFlags[args[1]] = flags })
	case "features":
		// fake features blob.json — the project's features, written out in the
		// script so the fixture a case turns on is visible next to it.
		items := scriptRows(ts, args[1])
		set(func() { f.features["101"] = items })
	case "segment-override":
		// max_items alone, with or without an override for segment 12.
		withSegmentOverride(f, args[1] == "on")
	case "feature-overrides":
		// max_items with two segment overrides and their feature-states, in
		// priority order — the fixture the override views are read against.
		withSegmentOverride(f, true)
		withFeatureOverridesRows(f)
	case "feature-segments":
		// fake feature-segments 2 rows.json
		id, err := strconv.Atoi(args[1])
		ts.Check(err)
		withFeatureSegments(f, id, scriptRows(ts, args[2])...)
	case "feature-states":
		// fake feature-states 2 rows.json
		id, err := strconv.Atoi(args[1])
		ts.Check(err)
		withFeatureStates(f, id, scriptRows(ts, args[2])...)
	case "identity-overrides":
		// fake identity-overrides rows.json — one row per identity's override
		// of one feature, as the core (non-edge) endpoints serve them.
		var rows []struct {
			Identifier string `json:"identifier"`
			ID         int    `json:"id"`
			Feature    int    `json:"feature"`
			Enabled    bool   `json:"enabled"`
			Value      any    `json:"value"`
		}
		ts.Check(json.Unmarshal([]byte(ts.ReadFile(args[1])), &rows))
		set(func() {
			f.coreIdentities = map[string]int{}
			f.coreOverrides = map[int]map[int]*fakeFS{}
			for i, r := range rows {
				f.coreIdentities[r.Identifier] = r.ID
				f.coreOverrides[r.ID] = map[int]*fakeFS{
					r.Feature: {id: 9100 + i, enabled: r.Enabled, value: r.Value},
				}
			}
		})
	case "edge-overrides":
		// The same, for a project that keeps its identities at the edge.
		var rows []struct {
			Identifier string `json:"identifier"`
			Feature    int    `json:"feature"`
			Enabled    bool   `json:"enabled"`
			Value      any    `json:"value"`
		}
		ts.Check(json.Unmarshal([]byte(ts.ReadFile(args[1])), &rows))
		set(func() {
			f.useEdge = true
			f.edgeOverrides = map[string]map[int]*fakeFS{}
			for _, r := range rows {
				f.edgeOverrides[r.Identifier] = map[int]*fakeFS{
					r.Feature: {enabled: r.Enabled, value: r.Value},
				}
			}
		})
	case "features-default":
		// The project's usual two features, as newFake starts with.
		set(func() { f.features["101"] = defaultFeatures() })
	case "projects":
		// fake projects 3 acme.json — the projects an organisation holds.
		rows := scriptRows(ts, args[2])
		set(func() { f.projects[args[1]] = rows })
	case "project-environments":
		// fake project-environments 202 envs.json — one project's environments,
		// for the cases that pick a project other than the default one.
		rows := scriptRows(ts, args[2])
		set(func() { f.envs[args[1]] = rows })
	case "server-keys":
		// fake server-keys K2mVsGdXhZ8kQqZ9pJmNbJ keys.json — the server-side
		// keys an environment already has.
		rows := scriptRows(ts, args[2])
		set(func() { f.serverKeys[args[1]] = rows })
	case "forget-requests":
		// The request log runs the length of a script, so a case that counts
		// calls says where its own counting starts.
		set(func() { f.reqLog = nil })
	case "workflow-gated":
		withWorkflowGating(f)
	case "segment-override-missing":
		withMissingSegmentOverride(f)
	case "edge-identities":
		withEdgeIdentities(f)
	case "orgs":
		// fake orgs Acme=3 Beta=7 — or no pairs at all for an instance the
		// credential can see no organisations in.
		var orgs []map[string]any
		for _, pair := range args[1:] {
			name, id, ok := strings.Cut(pair, "=")
			if !ok {
				ts.Fatalf("fake orgs: %q is not name=id", pair)
			}
			n, err := strconv.Atoi(id)
			ts.Check(err)
			orgs = append(orgs, map[string]any{"id": n, "name": name})
		}
		set(func() { f.orgs = orgs })
	case "org-fields":
		// Extra API fields on Acme, for the cases about what --json passes through.
		var n int
		set(func() { n = len(f.orgs) })
		if n == 0 {
			ts.Fatalf("fake org-fields: no organisations to add them to")
		}
		for _, pair := range args[1:] {
			k, v, ok := strings.Cut(pair, "=")
			if !ok {
				ts.Fatalf("fake org-fields: %q is not key=value", pair)
			}
			set(func() { f.orgs[0][k] = scriptValue(v) })
		}
	case "environments-named":
		// fake environments-named Staging=keyA Staging=keyB — for the cases
		// about names that do not identify one environment on their own.
		var envs []map[string]any
		for i, pair := range args[1:] {
			name, key, ok := strings.Cut(pair, "=")
			if !ok {
				ts.Fatalf("fake environments-named: %q is not name=key", pair)
			}
			envs = append(envs, map[string]any{
				"id": i + 1, "name": name, "api_key": key, "project": 101,
			})
		}
		set(func() { f.envs["101"] = envs })
	case "environments":
		// The two environments the Admin API knows about for project 101.
		set(func() {
			f.envs["101"] = []map[string]any{
				{"id": 1, "name": "Development", "api_key": "WqXhZk8sVY3dGgTqZ9pJmN", "project": 101, "description": "Local dev"},
				{"id": 2, "name": "Production", "api_key": "K2mVsGdXhZ8kQqZ9pJmNbJ", "project": 101, "description": "Live", "use_v2_feature_versioning": true},
			}
		})
	default:
		ts.Fatalf("unknown fake setting %q", args[0])
	}
}

// scriptJSON decodes a fixture file the way the Go fixtures are written:
// encoding/json makes every number a float64, but the fake compares ids and
// counts against int, so a whole number decodes to int here.
func scriptJSON(ts *testscript.TestScript, file string, into any) {
	ts.Check(json.Unmarshal([]byte(ts.ReadFile(file)), into))
}

// wholeNumbersToInt rewrites float64 values that are whole numbers as int,
// throughout a decoded document.
func wholeNumbersToInt(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, item := range t {
			t[k] = wholeNumbersToInt(item)
		}
	case []any:
		for i, item := range t {
			t[i] = wholeNumbersToInt(item)
		}
	case float64:
		if t == float64(int(t)) {
			return int(t)
		}
	}
	return v
}

// scriptRows decodes a fixture file of API rows.
func scriptRows(ts *testscript.TestScript, file string) []map[string]any {
	var rows []map[string]any
	scriptJSON(ts, file, &rows)
	for _, row := range rows {
		wholeNumbersToInt(row)
	}
	return rows
}

// scriptValue types a fixture value written in a script the way the API would
// carry it, so `fake org-fields force_2fa=true` sets a boolean and not "true".
func scriptValue(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	return s
}

// cmdSubst expands environment variables in a file, in place. txtar bodies are
// copied out verbatim, so a fixture that has to name the fake writes $API and
// says so:
//
//	subst config.json
func cmdSubst(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) == 0 {
		ts.Fatalf("usage: subst <file>...")
	}
	for _, name := range args {
		path := ts.MkAbs(name)
		body, err := os.ReadFile(path)
		ts.Check(err)
		expanded := os.Expand(string(body), func(k string) string { return ts.Getenv(k) })
		ts.Check(os.WriteFile(path, []byte(expanded), 0o644))
	}
}

// cmdCache seeds the local name cache, for the cases that turn on what the CLI
// already knows without asking the Admin API.
//
//	cache environments K2mVsGdXhZ8kQqZ9pJmNbJ=Production
func cmdCache(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) == 0 {
		ts.Fatalf("usage: cache [-url=<instance>] <organisations|projects|environments|segments> [<key>=<name>...]")
	}
	instance := ts.Value("fake").(*fakeInstance).srv.URL
	if strings.HasPrefix(args[0], "-url=") {
		instance = strings.TrimPrefix(args[0], "-url=")
		args = args[1:]
	}
	names := map[string]string{}
	for _, pair := range args[1:] {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			ts.Fatalf("cache: %q is not key=name", pair)
		}
		names[k] = v
	}

	// Merge, so a case can seed more than one kind of name.
	path := scriptCachePath(ts)
	all := map[string]*cache.Names{}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &all)
	}
	key := strings.TrimRight(instance, "/")
	if all[key] == nil {
		all[key] = &cache.Names{}
	}
	switch args[0] {
	case "organisations":
		all[key].Organisations = names
	case "projects":
		all[key].Projects = names
	case "environments":
		all[key].Environments = names
	case "segments":
		all[key].Segments = names
	default:
		ts.Fatalf("unknown cache kind %q", args[0])
	}
	body, err := json.Marshal(all)
	ts.Check(err)
	ts.Check(os.MkdirAll(filepath.Dir(path), 0o755))
	ts.Check(os.WriteFile(path, body, 0o600))
}

// cachePathFor is where the CLI under test keeps its name cache: what
// os.UserCacheDir resolves to for the script's HOME, not for this process's.
// Writing it via cache.Merge would target the developer's own cache directory.
func cachePathFor(home, xdg string) string {
	dir := filepath.Join(home, ".cache")
	switch {
	case runtime.GOOS == "darwin":
		dir = filepath.Join(home, "Library", "Caches")
	case runtime.GOOS == "windows":
		// os.UserCacheDir reads %LocalAppData% here, which Setup points at the
		// script's own directory.
		dir = filepath.Join(home, "AppData", "Local")
	case xdg != "":
		dir = xdg
	}
	return filepath.Join(dir, "flagsmith", "cache.json")
}

func scriptCachePath(ts *testscript.TestScript) string {
	return cachePathFor(ts.Getenv("HOME"), ts.Getenv("XDG_CACHE_HOME"))
}

// cmdDump writes a slice of what the fake observed to a file, so a script can
// assert on it with cmp — which means -update-scripts maintains it too.
//
//	exec flagsmith evaluate
//	dump requests requests.txt
//	cmpenv requests.txt expect-requests
func cmdDump(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) != 2 {
		ts.Fatalf("usage: dump <requests|sdk-agents|sdk-keys|env-lists> <file>")
	}
	f := ts.Value("fake").(*fakeInstance)
	var lines []string
	switch args[0] {
	case "requests":
		// Sorted: reads fan out concurrently, so arrival order is not a
		// property of the code — only which requests were made, and which
		// were not.
		lines = f.requests()
		sort.Strings(lines)
	case "sdk-agents":
		lines = f.sdkAgents()
	case "sdk-keys":
		lines = f.sdkSentKeys()
	case "env-lists":
		lines = []string{strconv.Itoa(f.environmentLists()) + " environment list calls"}
	default:
		ts.Fatalf("unknown dump %q", args[0])
	}
	out := ""
	if len(lines) > 0 {
		out = strings.Join(lines, "\n") + "\n"
	}
	if err := os.WriteFile(ts.MkAbs(args[1]), []byte(out), 0o644); err != nil {
		ts.Fatalf("writing %s: %v", args[1], err)
	}
	fmt.Fprintf(ts.Stdout(), "%d %s\n", len(lines), args[0])
}
