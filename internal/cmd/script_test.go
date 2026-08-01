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
			Execute()
		},
	})
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

	// An `env` in a script lasts to the end of that script, so a case that
	// clears the credential changes what its neighbours start from. $MASTER_KEY
	// lets a case put it back rather than depend on what ran before it.
	env.Setenv("MASTER_KEY", masterKey)
	env.Setenv("HOME", env.WorkDir)
	env.Setenv("XDG_CONFIG_HOME", filepath.Join(env.WorkDir, ".config"))
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
	if len(args) < 2 && !slices.Contains([]string{"environments", "orgs"}, args[0]) {
		ts.Fatalf("fake %s needs a value", args[0])
	}
	f := ts.Value("fake").(*fakeInstance)
	f.mu.Lock()
	defer f.mu.Unlock()
	switch args[0] {
	case "sdk-status":
		code, err := strconv.Atoi(args[1])
		ts.Check(err)
		f.sdkStatus = code
	case "sdk-delay":
		d, err := time.ParseDuration(args[1])
		ts.Check(err)
		f.sdkDelay = d
	case "sdk-flags":
		flags := sdkFlagsFrom(defaultFeatures())
		switch {
		case len(args) > 2 && args[2] == "none":
			flags = []map[string]any{}
		case len(args) > 2 && args[2] == "unknown":
			// Not in the map at all: the SDK API 401s, as it does for a key
			// that belongs to no environment.
			delete(f.sdkEnvFlags, args[1])
			return
		}
		f.sdkEnvFlags[args[1]] = flags
	case "sdk-identity":
		// This identity resolves max_items on at 99, so its own flags are
		// distinguishable from the environment's defaults.
		flags := sdkFlagsFrom(defaultFeatures())
		flags[1]["enabled"], flags[1]["feature_state_value"] = true, 99
		f.sdkIdentityFlags[args[1]] = flags
	case "orgs":
		// fake orgs Acme=3 Beta=7 — or no pairs at all for an instance the
		// credential can see no organisations in.
		f.orgs = nil
		for _, pair := range args[1:] {
			name, id, ok := strings.Cut(pair, "=")
			if !ok {
				ts.Fatalf("fake orgs: %q is not name=id", pair)
			}
			n, err := strconv.Atoi(id)
			ts.Check(err)
			f.orgs = append(f.orgs, map[string]any{"id": n, "name": name})
		}
	case "org-fields":
		// Extra API fields on Acme, for the cases about what --json passes through.
		if len(f.orgs) == 0 {
			ts.Fatalf("fake org-fields: no organisations to add them to")
		}
		for _, pair := range args[1:] {
			k, v, ok := strings.Cut(pair, "=")
			if !ok {
				ts.Fatalf("fake org-fields: %q is not key=value", pair)
			}
			f.orgs[0][k] = scriptValue(v)
		}
	case "environments":
		// The two environments the Admin API knows about for project 101.
		f.envs["101"] = []map[string]any{
			{"id": 1, "name": "Development", "api_key": "WqXhZk8sVY3dGgTqZ9pJmN", "project": 101, "description": "Local dev"},
			{"id": 2, "name": "Production", "api_key": "K2mVsGdXhZ8kQqZ9pJmNbJ", "project": 101, "description": "Live", "use_v2_feature_versioning": true},
		}
	default:
		ts.Fatalf("unknown fake setting %q", args[0])
	}
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

// cmdCache seeds the local name cache, for the cases that turn on what the CLI
// already knows without asking the Admin API.
//
//	cache environments K2mVsGdXhZ8kQqZ9pJmNbJ=Production
func cmdCache(ts *testscript.TestScript, neg bool, args []string) {
	if neg || len(args) < 2 || args[0] != "environments" {
		ts.Fatalf("usage: cache environments <key>=<name>...")
	}
	f := ts.Value("fake").(*fakeInstance)
	names := map[string]string{}
	for _, pair := range args[1:] {
		k, v, ok := strings.Cut(pair, "=")
		if !ok {
			ts.Fatalf("cache: %q is not key=name", pair)
		}
		names[k] = v
	}
	body, err := json.Marshal(map[string]*cache.Names{
		strings.TrimRight(f.srv.URL, "/"): {Environments: names},
	})
	ts.Check(err)
	path := scriptCachePath(ts)
	ts.Check(os.MkdirAll(filepath.Dir(path), 0o755))
	ts.Check(os.WriteFile(path, body, 0o600))
}

// scriptCachePath is where the CLI under test keeps its name cache: what
// os.UserCacheDir resolves to for the script's HOME, not for this process's.
// Writing it via cache.Merge would target the developer's own cache directory.
func scriptCachePath(ts *testscript.TestScript) string {
	dir := filepath.Join(ts.Getenv("HOME"), ".cache")
	switch {
	case runtime.GOOS == "darwin":
		dir = filepath.Join(ts.Getenv("HOME"), "Library", "Caches")
	case ts.Getenv("XDG_CACHE_HOME") != "":
		dir = ts.Getenv("XDG_CACHE_HOME")
	}
	return filepath.Join(dir, "flagsmith", "cache.json")
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
