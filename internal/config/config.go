// Package config finds and reads the repo-local flagsmith.json.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// FileName is the project config file discovered in the working tree.
const FileName = "flagsmith.json"

// ErrServerSideKey means the file's `environment` field holds a server-side
// (ser.) key, which is a secret and must not be committed. The recovery
// (use FLAGSMITH_ENVIRONMENT_KEY) is attached as a hint at the command layer
// (see internal/cmd hintFor), not baked into the message.
var ErrServerSideKey = errors.New("`environment` holds a server-side key, which is a secret")

// File is the parsed flagsmith.json. Zero values mean "not set".
type File struct {
	Schema       string `json:"$schema,omitempty"`
	Project      *Ref   `json:"project,omitempty"`
	Organisation *Ref   `json:"organisation,omitempty"`
	Environment  string `json:"environment,omitempty"`
	APIURL       string `json:"apiUrl,omitempty"`
	SDKAPIURL    string `json:"sdkApiUrl,omitempty"`

	// Extra holds fields the CLI does not recognise, captured verbatim by
	// Load so they survive a Save round trip instead of being silently
	// dropped (a newer file edited by an older CLI, say). Never marshalled
	// via the struct tags — MarshalJSON re-emits it after the known fields.
	Extra map[string]json.RawMessage `json:"-"`
}

// MarshalJSON emits the known fields in their declared order, then appends any
// Extra (unknown) fields so a Load/Save round trip preserves them.
func (f File) MarshalJSON() ([]byte, error) {
	type alias File // no MarshalJSON — plain struct-tag encoding
	known, err := json.Marshal(alias(f))
	if err != nil {
		return nil, err
	}
	if len(f.Extra) == 0 {
		return known, nil
	}
	keys := make([]string, 0, len(f.Extra))
	for k := range f.Extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var extra bytes.Buffer
	for _, k := range keys {
		if extra.Len() > 0 {
			extra.WriteByte(',')
		}
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		extra.Write(key)
		extra.WriteByte(':')
		extra.Write(f.Extra[k])
	}
	if bytes.Equal(bytes.TrimSpace(known), []byte("{}")) {
		return append([]byte("{"), append(extra.Bytes(), '}')...), nil
	}
	out := known[:len(known)-1] // drop the closing brace
	out = append(out, ',')
	out = append(out, extra.Bytes()...)
	return append(out, '}'), nil
}

// Save writes f to path as indented JSON, atomically: it writes a temp file in
// the same directory and renames it into place, so an interrupted write never
// leaves a truncated or partial flagsmith.json.
func (f *File) Save(path string) error {
	encoded, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".flagsmith-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds
	if _, err := tmp.Write(encoded); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Ref is a project or organisation reference: either a numeric ID or a name.
// An all-digit reference (given as a JSON number or a numeric string) is an
// ID; anything else is a name resolved via the Admin API. The nil *Ref means
// "not set".
type Ref struct {
	ID   int
	Name string
}

// Value returns the reference as the context layer wants it: an int ID, a
// string name, or nil when unset.
func (r *Ref) Value() any {
	switch {
	case r == nil:
		return nil
	case r.ID != 0:
		return r.ID
	case r.Name != "":
		return r.Name
	default:
		return nil
	}
}

func (r *Ref) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if n, err := strconv.Atoi(s); err == nil { // "12345" → ID
			r.ID = n
			return nil
		}
		r.Name = s
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	r.ID = n
	return nil
}

func (r Ref) MarshalJSON() ([]byte, error) {
	if r.ID != 0 {
		return json.Marshal(r.ID)
	}
	if r.Name != "" {
		return json.Marshal(r.Name)
	}
	return []byte("null"), nil
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
		return nil, nil, fmt.Errorf("%s: %w", path, ErrServerSideKey)
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	var warnings []string
	for field, raw := range all {
		if !knownFields[field] {
			warnings = append(warnings, fmt.Sprintf("unknown field %q in %s", field, path))
			if f.Extra == nil {
				f.Extra = map[string]json.RawMessage{}
			}
			f.Extra[field] = raw
		}
	}
	sort.Strings(warnings)
	return f, warnings, nil
}
