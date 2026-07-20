// Package cache is the local name cache: display names for organisations,
// projects (stored as IDs) and environments (stored as client-side keys).
// Strictly cosmetic — never consulted for authorisation or resolution; a
// miss degrades to showing the bare ID/key.
package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// Names maps identifiers to display names for one Flagsmith instance.
// Map keys are stringified IDs (organisations, projects) or client-side
// keys (environments).
type Names struct {
	Organisations map[string]string `json:"organisations,omitempty"`
	Projects      map[string]string `json:"projects,omitempty"`
	Environments  map[string]string `json:"environments,omitempty"`
}

// Path is the cache file, keyed by instance API URL.
func Path() (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "flagsmith", "cache.json"), nil
}

func instanceKey(apiURL string) string {
	return strings.TrimRight(apiURL, "/")
}

func readAll() map[string]*Names {
	path, err := Path()
	if err != nil {
		return map[string]*Names{}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]*Names{}
	}
	all := map[string]*Names{}
	if err := json.Unmarshal(raw, &all); err != nil {
		return map[string]*Names{}
	}
	return all
}

// Load returns the cached names for an instance; empty (never nil) on any
// miss or error — the cache is best-effort by design.
func Load(apiURL string) *Names {
	if names := readAll()[instanceKey(apiURL)]; names != nil {
		return names
	}
	return &Names{}
}

// Merge folds update into the cached names for an instance.
func Merge(apiURL string, update *Names) error {
	all := readAll()
	key := instanceKey(apiURL)
	names := all[key]
	if names == nil {
		names = &Names{}
		all[key] = names
	}
	merge := func(dst *map[string]string, src map[string]string) {
		if len(src) == 0 {
			return
		}
		if *dst == nil {
			*dst = map[string]string{}
		}
		for k, v := range src {
			(*dst)[k] = v
		}
	}
	merge(&names.Organisations, update.Organisations)
	merge(&names.Projects, update.Projects)
	merge(&names.Environments, update.Environments)

	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(all)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
