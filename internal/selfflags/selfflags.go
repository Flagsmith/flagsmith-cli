// Package selfflags evaluates the CLI's own feature flags.
package selfflags

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	flagsmith "github.com/Flagsmith/flagsmith-go-client/v5"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/httpx"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/version"
)

// LoadingAnimation gates the waving flag drawn while HTTP requests are in flight.
const LoadingAnimation = "loading_animation"

const (
	// environmentKey identifies the CLI's own Flagsmith environment
	environmentKey = "ESMtZFh4fZvWbfLeBgwiPm"

	// ttl is how long a cached evaluation is used before a refresh is attempted
	ttl = 6 * time.Hour
)

// baseURL is the SDK API these flags are evaluated against as the SDK wants it.
var baseURL = "https://edge.api.flagsmith.com/api/v1/"

// Audience is what a segment might target about this installation that this
// package cannot know on its own.
type Audience struct {
	// Organisation is the resolved organisation id.
	Organisation string

	// IsSaas is false when the CLI is pointed at an instance other than
	// Flagsmith's own.
	IsSaas bool
}

// traits describe this installation to Flagsmith.
func traits(aud Audience) []*flagsmith.Trait {
	t := []*flagsmith.Trait{
		{TraitKey: "cli.version", TraitValue: version.Version},
		{TraitKey: "os", TraitValue: runtime.GOOS},
		{TraitKey: "arch", TraitValue: runtime.GOARCH},
		{TraitKey: "is_saas", TraitValue: aud.IsSaas},
	}
	if aud.Organisation != "" {
		t = append(t, &flagsmith.Trait{TraitKey: "organisation.id", TraitValue: aud.Organisation})
	}
	return t
}

const idPrefix = "cli-"

// idPath is where the targeting key is kept.
func idPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "flagsmith", "install-id"), nil
}

// installID is the identifier this installation is evaluated as, created on first
// use and kept thereafter.
func installID() (string, error) {
	p, err := idPath()
	if err != nil {
		return "", err
	}
	if id := readID(p); id != "" {
		return id, nil
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return "", err
	}
	// O_EXCL, then read back on failure: two invocations racing to be the first
	// must agree which id won, or they would evaluate as two installations.
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if id := readID(p); id != "" {
			return id, nil
		}
		return "", err
	}
	defer f.Close()
	id := idPrefix + randomID()
	if _, err := f.WriteString(id); err != nil {
		return "", err
	}
	return id, nil
}

func readID(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func randomID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing is not recoverable
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// cached is the on-disk evaluation. FetchedAt drives the ttl; a zero value (or
// an unparseable file) reads as "never fetched", so a refresh will retry.
type cached struct {
	Flags     map[string]bool `json:"flags"`
	FetchedAt time.Time       `json:"fetchedAt"`
}

func path() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "flagsmith", "selfflags.json"), nil
}

// load reads the cache, degrading to an empty evaluation on any error: an
// unreadable cosmetic cache must never fail a command.
func load() cached {
	p, err := path()
	if err != nil {
		return cached{}
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return cached{}
	}
	var c cached
	if err := json.Unmarshal(raw, &c); err != nil {
		return cached{}
	}
	return c
}

func store(c cached) error {
	p, err := path()
	if err != nil {
		return err
	}
	// Owner-only, like the name cache beside it: nothing else on the machine
	// needs to know what this CLI draws.
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(p, raw, 0o600)
}

// Enabled reports whether the named flag was on the last time a refresh
// succeeded. A cold cache is false, matching how these flags are created: off
// until deliberately turned on. A stale value is kept until a refresh replaces
// it — flickering with every network hiccup would be worse than answering with
// yesterday's truth.
func Enabled(name string) bool {
	return load().Flags[name]
}

// Refresh evaluates the CLI's flags and caches the result, doing nothing if the
// cached one is younger than the ttl. The error is for tests and tracing:
// callers run this for its effect on the next invocation, and have nothing to do
// when it fails.
func Refresh(ctx context.Context, aud Audience) error {
	if c := load(); !c.FetchedAt.IsZero() && time.Since(c.FetchedAt) < ttl {
		return nil
	}
	flags, err := evaluate(ctx, aud)
	if err != nil {
		return err
	}
	return store(cached{Flags: flags, FetchedAt: time.Now()})
}

// evaluate resolves this installation's flags through the Flagsmith SDK
func evaluate(ctx context.Context, aud Audience) (map[string]bool, error) {
	id, err := installID()
	if err != nil {
		return nil, err
	}
	hc := httpx.New(version.UserAgent())
	client := flagsmith.NewClient(environmentKey,
		flagsmith.WithBaseURL(baseURL),
		flagsmith.WithHTTPClient(hc),
		flagsmith.WithSlogLogger(slog.New(slog.DiscardHandler)),
	)
	hc.Timeout = 0
	resolved, err := client.GetIdentityFlagsFromAPI(ctx, id, traits(aud))
	if err != nil {
		return nil, err
	}
	evaluated := resolved.AllFlags()
	// No flags at all would turn everything off. The CLI's own project always has
	// some, so read it as an answer from somewhere unexpected rather than
	// overwriting a working cache with nothing.
	if len(evaluated) == 0 {
		return nil, errors.New("evaluating the CLI's own flags returned no flags")
	}
	flags := make(map[string]bool, len(evaluated))
	for _, e := range evaluated {
		if e.FeatureName != "" {
			flags[e.FeatureName] = e.Enabled
		}
	}
	return flags, nil
}
