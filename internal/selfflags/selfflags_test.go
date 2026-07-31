package selfflags

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/version"
)

func isolate(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, ".cache"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	t.Setenv("LocalAppData", tmp)
	t.Setenv("AppData", tmp)
}

// evaluation is one identity evaluation as the stub received it.
type evaluation struct {
	Key        string
	Identifier string
	Traits     map[string]any
}

// stubSDK serves identity evaluations and records what was asked of it.
func stubSDK(t *testing.T, body string, status int) *[]evaluation {
	t.Helper()
	got := new([]evaluation)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/identities/" {
			t.Errorf("%s %s, want POST /api/v1/identities/", r.Method, r.URL.Path)
		}
		var sent struct {
			Identifier string `json:"identifier"`
			Traits     []struct {
				Key   string `json:"trait_key"`
				Value any    `json:"trait_value"`
			} `json:"traits"`
		}
		if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
			t.Errorf("decoding the evaluation request: %v", err)
		}
		e := evaluation{Key: r.Header.Get("X-Environment-Key"), Identifier: sent.Identifier, Traits: map[string]any{}}
		for _, tr := range sent.Traits {
			e.Traits[tr.Key] = tr.Value
		}
		*got = append(*got, e)
		w.WriteHeader(status)
		w.Write([]byte(body)) //nolint:errcheck
	}))
	t.Cleanup(srv.Close)
	previous := baseURL
	baseURL = srv.URL + "/api/v1/"
	t.Cleanup(func() { baseURL = previous })
	return got
}

const bothFlags = `{"flags": [
	{"enabled": true, "feature": {"name": "loading_animation"}},
	{"enabled": false, "feature": {"name": "something_else"}}
], "traits": []}`

func TestEnabledColdCache(t *testing.T) {
	// Given no cache has ever been written
	isolate(t)

	// Then a flag nobody has evaluated is off, matching how it was created
	if Enabled(LoadingAnimation) {
		t.Error("Enabled on a cold cache = true, want false")
	}
}

func TestRefreshCachesEvaluation(t *testing.T) {
	// Given the SDK API says the animation is on
	isolate(t)
	got := stubSDK(t, bothFlags, http.StatusOK)

	// When the cache is refreshed
	if err := Refresh(context.Background(), Audience{}); err != nil {
		t.Fatal(err)
	}

	// Then the evaluation is readable without a network call, per flag
	if !Enabled(LoadingAnimation) {
		t.Error("Enabled(LoadingAnimation) = false, want true")
	}
	if Enabled("something_else") {
		t.Error(`Enabled("something_else") = true, want false`)
	}
	if Enabled("never_heard_of_it") {
		t.Error("an unevaluated flag reported enabled")
	}
	// And the request identified the CLI's own environment
	if len(*got) != 1 || (*got)[0].Key != environmentKey {
		t.Errorf("evaluations = %+v, want one carrying key %q", *got, environmentKey)
	}
}

func TestRefreshSkipsFreshCache(t *testing.T) {
	// Given a cache written moments ago
	isolate(t)
	got := stubSDK(t, bothFlags, http.StatusOK)
	if err := Refresh(context.Background(), Audience{}); err != nil {
		t.Fatal(err)
	}

	// When it is refreshed again
	if err := Refresh(context.Background(), Audience{}); err != nil {
		t.Fatal(err)
	}

	// Then no second request was made: once per ttl is enough for a cosmetic flag
	if len(*got) != 1 {
		t.Errorf("requests = %d, want 1", len(*got))
	}
}

func TestRefreshRefetchesStaleCache(t *testing.T) {
	// Given a cache older than the ttl
	isolate(t)
	got := stubSDK(t, bothFlags, http.StatusOK)
	if err := store(cached{
		Flags:     map[string]bool{LoadingAnimation: false},
		FetchedAt: time.Now().Add(-2 * ttl),
	}); err != nil {
		t.Fatal(err)
	}

	// When it is refreshed
	if err := Refresh(context.Background(), Audience{}); err != nil {
		t.Fatal(err)
	}

	// Then the stale value was replaced
	if len(*got) != 1 {
		t.Errorf("requests = %d, want 1", len(*got))
	}
	if !Enabled(LoadingAnimation) {
		t.Error("stale value survived a successful refresh")
	}
}

func TestEnabledKeepsStaleValueWhenRefreshFails(t *testing.T) {
	// Given a stale cache and an SDK API that is down
	isolate(t)
	if err := store(cached{
		Flags:     map[string]bool{LoadingAnimation: true},
		FetchedAt: time.Now().Add(-2 * ttl),
	}); err != nil {
		t.Fatal(err)
	}
	stubSDK(t, "nope", http.StatusInternalServerError)

	// When the refresh fails
	if err := Refresh(context.Background(), Audience{}); err == nil {
		t.Fatal("Refresh on a 500 returned no error")
	}

	// Then the last answer is still used: yesterday's truth beats flickering
	if !Enabled(LoadingAnimation) {
		t.Error("a failed refresh discarded the cached value")
	}
}

func TestRefreshRejectsMalformedResponse(t *testing.T) {
	// Given an SDK API returning something that is not an evaluation
	isolate(t)
	stubSDK(t, `{"detail": "Invalid environment key"}`, http.StatusOK)

	// When the cache is refreshed
	err := Refresh(context.Background(), Audience{})

	// Then it fails rather than caching an empty evaluation
	if err == nil {
		t.Fatal("Refresh on a malformed body returned no error")
	}
	if _, statErr := os.Stat(mustPath(t)); !os.IsNotExist(statErr) {
		t.Error("a malformed response was cached")
	}
}

func TestCacheIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX file modes")
	}
	// Given a refreshed cache
	isolate(t)
	stubSDK(t, bothFlags, http.StatusOK)
	if err := Refresh(context.Background(), Audience{}); err != nil {
		t.Fatal(err)
	}

	// Then nothing else on the machine can read it
	info, err := os.Stat(mustPath(t))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

func TestEnabledIgnoresUnreadableCache(t *testing.T) {
	// Given a cache file that is not JSON
	isolate(t)
	path := mustPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{{{"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Then reading it degrades to off rather than failing the command
	if Enabled(LoadingAnimation) {
		t.Error("Enabled on a corrupt cache = true, want false")
	}
}

func TestRefreshReplacesCorruptCache(t *testing.T) {
	// Given a corrupt cache, which carries no usable fetch time
	isolate(t)
	path := mustPath(t)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := stubSDK(t, bothFlags, http.StatusOK)

	// When it is refreshed
	if err := Refresh(context.Background(), Audience{}); err != nil {
		t.Fatal(err)
	}

	// Then it was refetched, not treated as fresh
	if len(*got) != 1 {
		t.Errorf("requests = %d, want 1", len(*got))
	}
	if !Enabled(LoadingAnimation) {
		t.Error("Enabled after refreshing a corrupt cache = false, want true")
	}
}

func TestBaseURLIsFlagsmithEdge(t *testing.T) {
	// The CLI's own flags live in Flagsmith's project, so nothing the user
	// configures may redirect this request at their instance.
	if baseURL != "https://edge.api.flagsmith.com/api/v1/" {
		t.Errorf("baseURL = %q", baseURL)
	}
}

// mustPath is the cache path under the isolated HOME.
func mustPath(t *testing.T) string {
	t.Helper()
	p, err := path()
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// TestCachedRoundTrip pins the on-disk shape: an older CLI's cache must not
// confuse a newer one, and vice versa.
func TestCachedRoundTrip(t *testing.T) {
	raw, err := json.Marshal(cached{Flags: map[string]bool{LoadingAnimation: true}, FetchedAt: time.Unix(0, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"flags":{"loading_animation":true},"fetchedAt":"1970-01-01T00:00:00Z"}`
	if string(raw) != want {
		t.Errorf("cached JSON =\n%s\nwant\n%s", raw, want)
	}
}

func TestEvaluatesAsAStableInstall(t *testing.T) {
	// Given an installation that has evaluated once
	isolate(t)
	got := stubSDK(t, bothFlags, http.StatusOK)
	if err := Refresh(context.Background(), Audience{}); err != nil {
		t.Fatal(err)
	}

	// When it evaluates again, the cache having gone stale in between
	if err := store(cached{Flags: map[string]bool{}, FetchedAt: time.Now().Add(-2 * ttl)}); err != nil {
		t.Fatal(err)
	}
	if err := Refresh(context.Background(), Audience{}); err != nil {
		t.Fatal(err)
	}

	// Then both evaluations used the same identifier: a percentage rollout has to
	// hold still across invocations, or the flag would flicker on and off
	if len(*got) != 2 {
		t.Fatalf("evaluations = %d, want 2", len(*got))
	}
	first, second := (*got)[0].Identifier, (*got)[1].Identifier
	if first != second {
		t.Errorf("identifier changed between evaluations: %q then %q", first, second)
	}
	// And it is marked as the CLI's own, among a project's application identities
	if !strings.HasPrefix(first, idPrefix) {
		t.Errorf("identifier = %q, want it prefixed %q", first, idPrefix)
	}
	// And it is random, not the machine or the user: nothing recognisable
	for _, leak := range []string{os.Getenv("USER"), hostname(t)} {
		if leak != "" && strings.Contains(first, leak) {
			t.Errorf("identifier %q carries %q", first, leak)
		}
	}
}

func TestInstallIDOutlivesTheCache(t *testing.T) {
	// Given an installation that has evaluated
	isolate(t)
	got := stubSDK(t, bothFlags, http.StatusOK)
	if err := Refresh(context.Background(), Audience{}); err != nil {
		t.Fatal(err)
	}

	// When the cache is thrown away entirely, as a cache may be
	if err := os.Remove(mustPath(t)); err != nil {
		t.Fatal(err)
	}
	if err := Refresh(context.Background(), Audience{}); err != nil {
		t.Fatal(err)
	}

	// Then the identifier survived it: the key lives in the config directory for
	// exactly this reason
	if (*got)[0].Identifier != (*got)[1].Identifier {
		t.Errorf("clearing the cache re-rolled the targeting key: %q then %q",
			(*got)[0].Identifier, (*got)[1].Identifier)
	}
}

func TestInstallIDIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX file modes")
	}
	// Given a fresh installation
	isolate(t)

	// When its targeting key is created
	if _, err := installID(); err != nil {
		t.Fatal(err)
	}

	// Then nothing else on the machine can read it
	p, err := idPath()
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

func TestEvaluationCarriesTargetableTraits(t *testing.T) {
	// Given a SaaS installation working in a known organisation
	isolate(t)
	got := stubSDK(t, bothFlags, http.StatusOK)

	// When it evaluates
	if err := Refresh(context.Background(), Audience{Organisation: "13", IsSaas: true}); err != nil {
		t.Fatal(err)
	}

	// Then a segment has the version, the platform and the deployment to target
	traits := (*got)[0].Traits
	for key, want := range map[string]any{
		"cli.version":     version.Version,
		"os":              runtime.GOOS,
		"arch":            runtime.GOARCH,
		"is_saas":         true,
		"organisation.id": "13",
	} {
		if traits[key] != want {
			t.Errorf("trait %s = %v, want %v", key, traits[key], want)
		}
	}
}

func TestUnknownOrganisationIsNotSent(t *testing.T) {
	// Given an installation with no organisation resolved to an id
	isolate(t)
	got := stubSDK(t, bothFlags, http.StatusOK)

	// When it evaluates
	if err := Refresh(context.Background(), Audience{}); err != nil {
		t.Fatal(err)
	}

	// Then the trait is absent rather than empty: a segment testing it should not
	// match an installation that never said
	if _, sent := (*got)[0].Traits["organisation.id"]; sent {
		t.Errorf("traits = %v, want no organisation", (*got)[0].Traits)
	}
	// And a false is still stated rather than left out, so self-hosted is
	// targetable and not merely the absence of SaaS
	if got, sent := (*got)[0].Traits["is_saas"]; !sent || got != false {
		t.Errorf("is_saas = %v (sent: %t), want false", got, sent)
	}
	// And what is always knowable is still there
	if (*got)[0].Traits["cli.version"] != version.Version {
		t.Errorf("cli.version = %v, want %q", (*got)[0].Traits["cli.version"], version.Version)
	}
}

func hostname(t *testing.T) string {
	t.Helper()
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}
