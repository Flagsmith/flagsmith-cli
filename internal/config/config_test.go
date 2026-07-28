package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestDiscover(t *testing.T) {
	t.Run("file in cwd", func(t *testing.T) {
		// Given
		root := gitRepo(t)
		write(t, filepath.Join(root, "flagsmith.json"), `{"project": 1}`)

		// When
		got, err := Discover(root)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if got != filepath.Join(root, "flagsmith.json") {
			t.Errorf("Discover = %q", got)
		}
	})

	t.Run("walks up to the git toplevel", func(t *testing.T) {
		// Given
		root := gitRepo(t)
		write(t, filepath.Join(root, "flagsmith.json"), `{"project": 1}`)
		nested := filepath.Join(root, "apps", "api", "handlers")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}

		// When
		got, err := Discover(nested)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if got != filepath.Join(root, "flagsmith.json") {
			t.Errorf("Discover = %q", got)
		}
	})

	t.Run("nearest file wins in a monorepo", func(t *testing.T) {
		// Given
		root := gitRepo(t)
		write(t, filepath.Join(root, "flagsmith.json"), `{"project": 1}`)
		write(t, filepath.Join(root, "apps", "api", "flagsmith.json"), `{"project": 2}`)

		// When
		got, err := Discover(filepath.Join(root, "apps", "api"))

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if got != filepath.Join(root, "apps", "api", "flagsmith.json") {
			t.Errorf("Discover = %q", got)
		}
	})

	t.Run("never walks past the git toplevel", func(t *testing.T) {
		// Given
		outer := t.TempDir()
		write(t, filepath.Join(outer, "flagsmith.json"), `{"project": 666}`)
		root := filepath.Join(outer, "repo")
		if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}

		// When
		got, err := Discover(root)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if got != "" {
			t.Errorf("Discover = %q, want nothing — the file lives outside the repo", got)
		}
	})

	t.Run("outside a git repo only cwd is checked", func(t *testing.T) {
		// Given
		dir := t.TempDir()
		write(t, filepath.Join(dir, "flagsmith.json"), `{"project": 666}`)
		nested := filepath.Join(dir, "sub")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}

		// When
		got, err := Discover(nested)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if got != "" {
			t.Errorf("Discover = %q, want nothing — no walking outside a repo", got)
		}
	})
}

func TestKnownFieldsMatchSchema(t *testing.T) {
	// Given
	raw, err := os.ReadFile("../../schema/flagsmith.json")
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}

	// When / Then
	for field := range schema.Properties {
		if !knownFields[field] {
			t.Errorf("schema field %q is unknown to the parser", field)
		}
	}
	for field := range knownFields {
		if _, ok := schema.Properties[field]; !ok {
			t.Errorf("parser field %q is not in the schema", field)
		}
	}
}

func TestLoad(t *testing.T) {
	t.Run("full file", func(t *testing.T) {
		// Given
		path := filepath.Join(t.TempDir(), "flagsmith.json")
		write(t, path, `{
			"$schema": "https://example.com/schema.json",
			"project": 12345,
			"organisation": 3,
			"environment": "WqXhZk8sVY3dGgTqZ9pJmN",
			"apiUrl": "https://flagsmith.acme.internal",
			"sdkApiUrl": "https://flags.acme.com"
		}`)

		// When
		f, warnings, err := Load(path)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if len(warnings) != 0 {
			t.Errorf("warnings = %v, want none", warnings)
		}
		if f.Project.ID != 12345 || f.Organisation.ID != 3 ||
			f.Environment != "WqXhZk8sVY3dGgTqZ9pJmN" ||
			f.APIURL != "https://flagsmith.acme.internal" ||
			f.SDKAPIURL != "https://flags.acme.com" {
			t.Errorf("file = %+v", f)
		}
	})

	t.Run("server-side key is rejected", func(t *testing.T) {
		// Given
		path := filepath.Join(t.TempDir(), "flagsmith.json")
		write(t, path, `{"environment": "ser.AbCdEf1234"}`)

		// When
		_, _, err := Load(path)

		// Then
		if !errors.Is(err, ErrServerSideKey) {
			t.Errorf("err = %v, want ErrServerSideKey", err)
		}
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("err = %v, want it to name %s", err, path)
		}
	})

	t.Run("unknown fields warn but load", func(t *testing.T) {
		// Given
		path := filepath.Join(t.TempDir(), "flagsmith.json")
		write(t, path, `{"project": 1, "enviroment": "typo"}`)

		// When
		f, warnings, err := Load(path)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if f.Project.ID != 1 {
			t.Errorf("Project = %d", f.Project.ID)
		}
		if len(warnings) != 1 || !strings.Contains(warnings[0], "enviroment") {
			t.Errorf("warnings = %v, want one about the unknown field", warnings)
		}
	})

	t.Run("malformed JSON errors", func(t *testing.T) {
		// Given
		path := filepath.Join(t.TempDir(), "flagsmith.json")
		write(t, path, `{not json`)

		// When / Then
		if _, _, err := Load(path); err == nil {
			t.Error("expected a parse error")
		}
	})
}

func TestSavePreservesUnknownFields(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "flagsmith.json")
	write(t, path, `{
		"$schema": "https://example.com/schema.json",
		"project": 1,
		"futureFlag": {"nested": true},
		"anotherUnknown": "keep me"
	}`)
	f, _, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	// When
	f.Environment = "WqXhZk8sVY3dGgTqZ9pJmN"
	if err := f.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Then
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("re-parsing saved file: %v\n%s", err, raw)
	}
	compact := func(b json.RawMessage) string {
		var buf bytes.Buffer
		if err := json.Compact(&buf, b); err != nil {
			t.Fatalf("compacting %s: %v", b, err)
		}
		return buf.String()
	}
	if compact(got["futureFlag"]) != `{"nested":true}` {
		t.Errorf("futureFlag = %s, want it preserved", got["futureFlag"])
	}
	if compact(got["anotherUnknown"]) != `"keep me"` {
		t.Errorf("anotherUnknown = %s, want it preserved", got["anotherUnknown"])
	}
	if compact(got["environment"]) != `"WqXhZk8sVY3dGgTqZ9pJmN"` {
		t.Errorf("environment = %s, want the updated value", got["environment"])
	}
}

func TestSaveIsAtomic(t *testing.T) {
	// Given
	dir := t.TempDir()
	path := filepath.Join(dir, "flagsmith.json")
	f := &File{Schema: "https://example.com/schema.json", Project: &Ref{ID: 7}}

	// When
	if err := f.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Then
	reread, _, err := Load(path)
	if err != nil {
		t.Fatalf("reloading saved file: %v", err)
	}
	if reread.Project.ID != 7 {
		t.Errorf("project = %d, want 7", reread.Project.ID)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 || raw[len(raw)-1] != '\n' {
		t.Errorf("file does not end in a newline: %q", raw)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("dir has %d entries, want only flagsmith.json (a temp file leaked): %v", len(entries), entries)
	}
}

func TestRefParsing(t *testing.T) {
	// Given / When / Then
	cases := []struct {
		name    string
		json    string
		wantID  int
		wantVal any
	}{
		{"json number", `{"project": 12345}`, 12345, 12345},
		{"numeric string", `{"project": "12345"}`, 12345, 12345},
		{"name", `{"project": "my-app"}`, 0, "my-app"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var f File
			if err := json.Unmarshal([]byte(tc.json), &f); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if f.Project.ID != tc.wantID {
				t.Errorf("ID = %d, want %d", f.Project.ID, tc.wantID)
			}
			if got := f.Project.Value(); got != tc.wantVal {
				t.Errorf("Value() = %v, want %v", got, tc.wantVal)
			}
		})
	}
}

func TestRefRoundTrip(t *testing.T) {
	// Given
	// When / Then
	id := &Ref{ID: 101}
	if b, _ := json.Marshal(id); string(b) != "101" {
		t.Errorf("ID marshalled to %s, want 101", b)
	}
	name := &Ref{Name: "acme-api"}
	if b, _ := json.Marshal(name); string(b) != `"acme-api"` {
		t.Errorf("name marshalled to %s, want \"acme-api\"", b)
	}
}
