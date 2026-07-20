// Package config finds and reads the repo-local flagsmith.json.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// FileName is the project config file discovered in the working tree.
const FileName = "flagsmith.json"

// File is the parsed flagsmith.json. Zero values mean "not set".
type File struct {
	Schema       string `json:"$schema,omitempty"`
	Project      int    `json:"project,omitempty"`
	Organisation int    `json:"organisation,omitempty"`
	Environment  string `json:"environment,omitempty"`
	APIURL       string `json:"apiUrl,omitempty"`
	SDKAPIURL    string `json:"sdkApiUrl,omitempty"`
}

var knownFields = map[string]bool{
	"$schema":      true,
	"project":      true,
	"organisation": true,
	"environment":  true,
	"apiUrl":       true,
	"sdkApiUrl":    true,
}

// Discover returns the path of the nearest flagsmith.json, walking up from
// dir to the git toplevel. Outside a git repository only dir itself is
// checked — a stray file higher up the tree never applies silently.
// Returns "" when no file is found.
func Discover(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	stop := dir
	if top, ok := gitToplevel(dir); ok {
		stop = top
	}
	for cur := dir; ; {
		path := filepath.Join(cur, FileName)
		if fi, err := os.Stat(path); err == nil && !fi.IsDir() {
			return path, nil
		}
		if cur == stop {
			return "", nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", nil
		}
		cur = parent
	}
}

// gitToplevel walks up from dir looking for a .git entry (directory, or
// file for worktrees and submodules).
func gitToplevel(dir string) (string, bool) {
	for cur := dir; ; {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		cur = parent
	}
}

// Load reads and validates a flagsmith.json. Unknown fields produce
// warnings, not errors, for forward compatibility; a server-side key in
// `environment` is an error — those are secrets and never belong in a
// committed file.
func Load(path string) (*File, []string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	f := &File{}
	if err := json.Unmarshal(raw, f); err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if strings.HasPrefix(f.Environment, "ser.") {
		return nil, nil, fmt.Errorf(
			"%s: environment holds a server-side key — server-side keys are secrets; provide them via FLAGSMITH_ENVIRONMENT_KEY instead", path)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	var warnings []string
	for field := range all {
		if !knownFields[field] {
			warnings = append(warnings, fmt.Sprintf("unknown field %q in %s", field, path))
		}
	}
	sort.Strings(warnings)
	return f, warnings, nil
}
