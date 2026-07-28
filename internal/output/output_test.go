package output

import (
	"bytes"
	"encoding/json"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/fatih/color"
)

func init() { color.NoColor = true }

type sample struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func TestRenderHumanByDefault(t *testing.T) {
	// Given
	var b bytes.Buffer
	called := false

	// When
	err := Render(&b, sample{1, "a"}, Options{}, func(w io.Writer) error {
		called = true
		w.Write([]byte("human view"))
		return nil
	})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if !called || b.String() != "human view" {
		t.Errorf("output = %q, called=%v", b.String(), called)
	}
}

func TestRenderJSONMirrorsData(t *testing.T) {
	// Given
	var b bytes.Buffer

	// When
	err := Render(&b, sample{7, "beta"}, Options{JSON: true}, func(io.Writer) error {
		t.Fatal("human renderer must not run in JSON mode")
		return nil
	})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	var got sample
	if err := json.Unmarshal(b.Bytes(), &got); err != nil {
		t.Fatalf("output %q is not the data shape: %v", b.String(), err)
	}
	if got != (sample{7, "beta"}) {
		t.Errorf("got %+v", got)
	}
}

func TestRenderJSONListIsBareArray(t *testing.T) {
	// Given
	var b bytes.Buffer

	// When
	err := Render(&b, []sample{{1, "a"}, {2, "b"}}, Options{JSON: true}, nil)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(b.String()), "[") {
		t.Errorf("output = %q, want a bare array", b.String())
	}
}

func TestRenderJQImpliesJSONAndFilters(t *testing.T) {
	t.Run("string field prints raw", func(t *testing.T) {
		// Given
		var b bytes.Buffer

		// When
		err := Render(&b, sample{7, "beta"}, Options{JQ: ".name"}, func(io.Writer) error {
			t.Fatal("human renderer must not run when --jq is set")
			return nil
		})

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(b.String()) != "beta" {
			t.Errorf("output = %q, want raw beta", b.String())
		}
	})

	t.Run("numeric field", func(t *testing.T) {
		// Given
		var b bytes.Buffer

		// When
		err := Render(&b, sample{7, "beta"}, Options{JQ: ".id"}, nil)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(b.String()) != "7" {
			t.Errorf("output = %q, want 7", b.String())
		}
	})

	t.Run("object result prints JSON", func(t *testing.T) {
		// Given
		var b bytes.Buffer

		// When
		err := Render(&b, []sample{{1, "a"}, {2, "b"}}, Options{JQ: ".[] | {n: .name}"}, nil)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(strings.TrimSpace(b.String()), "\n")
		if len(lines) != 2 || !strings.Contains(lines[0], `"n"`) {
			t.Errorf("output = %q, want two JSON objects", b.String())
		}
	})

	t.Run("invalid expression errors", func(t *testing.T) {
		// Given
		var b bytes.Buffer

		// When / Then
		if err := Render(&b, sample{1, "a"}, Options{JQ: "this is not jq"}, nil); err == nil {
			t.Error("expected a parse error for a bad jq expression")
		}
	})
}

func TestTable(t *testing.T) {
	// Given
	var b bytes.Buffer

	// When
	err := Table(&b, []string{"NAME", "ENABLED"}, [][]string{
		{"my-flag", "true"},
		{"other-flag", "false"},
	})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"NAME", "ENABLED", "my-flag", "other-flag", "false"} {
		if !strings.Contains(out, want) {
			t.Errorf("table = %q, want %q", out, want)
		}
	}
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestTableBoldHeaderKeepsAlignment(t *testing.T) {
	// Given
	headers := []string{"NAME", "ENABLED", "VALUE"}
	rows := [][]string{{"example_mv_feature", "false", "control"}, {"x", "true", "-"}}

	render := func() string {
		var b bytes.Buffer
		if err := Table(&b, headers, rows); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	color.NoColor = true
	plain := render()
	color.NoColor = false
	colored := render()
	color.NoColor = true

	// Then
	if !strings.Contains(colored, "\x1b[1m") {
		t.Error("expected a bold header when colour is on")
	}
	// ...the tabwriter escape byte never leaks...
	if strings.ContainsRune(colored, '\xff') {
		t.Errorf("escape byte leaked: %q", colored)
	}
	// ...and stripping the ANSI yields exactly the plain layout: colour is
	// applied after alignment, so it can't move columns.
	if got := ansiPattern.ReplaceAllString(colored, ""); got != plain {
		t.Errorf("colour changed alignment:\nstripped = %q\nplain    = %q", got, plain)
	}
	// Plain output has no escapes and aligns.
	if strings.Contains(plain, "\x1b") {
		t.Errorf("plain table contains escape bytes: %q", plain)
	}
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	if strings.Index(lines[0], "ENABLED") != strings.Index(lines[1], "false") {
		t.Errorf("columns not aligned:\n%q\n%q", lines[0], lines[1])
	}
}

func TestDetail(t *testing.T) {
	// Given
	var b bytes.Buffer

	// When
	err := Detail(&b, []Field{{Label: "Name", Value: "my-flag"}, {Label: "Enabled", Value: "true"}})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "Name") || !strings.Contains(out, "my-flag") {
		t.Errorf("detail = %q", out)
	}
}

func TestDetailWithSourceColumn(t *testing.T) {
	// Given
	fields := []Field{
		{Label: "Config file", Value: "/work/flagsmith.json", Source: "default"},
		{Label: "Project", Value: "my-app (12345)", Source: "config"},
		{Label: "SDK API", Value: "https://edge.api.flagsmith.com", Source: "default"},
	}
	render := func() string {
		var b bytes.Buffer
		if err := Detail(&b, fields); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	color.NoColor = true
	plain := render()
	color.NoColor = false
	colored := render()
	color.NoColor = true

	// Then
	lines := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	if strings.Index(lines[0], "default") != strings.Index(lines[2], "default") {
		t.Errorf("source column not aligned:\n%q\n%q", lines[0], lines[2])
	}
	if strings.Contains(plain, "\x1b") {
		t.Errorf("plain detail contains escape bytes: %q", plain)
	}
	// The label is coloured and the source dimmed; the space-containing value
	// (e.g. "my-app (12345)") is left untouched.
	if !strings.Contains(colored, "\x1b[36m") || !strings.Contains(colored, "\x1b[2m") {
		t.Errorf("expected cyan label and dim source: %q", colored)
	}
	if strings.Contains(colored, "\x1b[36mmy-app") || strings.Contains(colored, "\x1b[2mmy-app") {
		t.Errorf("value was coloured: %q", colored)
	}
	// Colour applied after alignment: stripping ANSI reproduces the plain layout.
	if got := ansiPattern.ReplaceAllString(colored, ""); got != plain {
		t.Errorf("colour changed alignment:\nstripped = %q\nplain    = %q", got, plain)
	}
}

// Column boundaries come from the known field content, not from scanning the
// padded line — so values containing runs of spaces colour correctly, and a
// row without a source emits no stray escape bytes.
func TestDetailColoursSurviveSpaceyValues(t *testing.T) {
	// Given
	fields := []Field{
		{Label: "Organisation", Value: "Acme  Inc", Source: "config"},
		{Label: "Trailing", Value: "ends with  ", Source: "env"},
		{Label: "NoSource", Value: "double  space", Source: ""},
	}
	var b bytes.Buffer
	color.NoColor = false
	err := Detail(&b, fields)
	color.NoColor = true
	if err != nil {
		t.Fatal(err)
	}
	colored := b.String()

	// Then
	if strings.Contains(colored, "\x1b[2mInc") || strings.Contains(colored, "\x1b[2mspace") {
		t.Errorf("value fragment dimmed as a source: %q", colored)
	}
	if !strings.Contains(colored, "\x1b[2mconfig\x1b[0m") || !strings.Contains(colored, "\x1b[2menv\x1b[0m") {
		t.Errorf("sources not dimmed exactly: %q", colored)
	}
	// And a row with no source carries no zero-width escape sequence
	if strings.Contains(colored, "\x1b[2m\x1b[0m") {
		t.Errorf("stray empty escape sequence on a sourceless row: %q", colored)
	}
}

func TestDetailFlattensStructuralWhitespace(t *testing.T) {
	// Given
	var b bytes.Buffer
	fields := []Field{
		{Label: "Feature", Value: "banner", Source: "config"},
		{Label: "Description", Value: "line one\nline two", Source: "config"},
		{Label: "Value", Value: "a\tb", Source: "env"},
		{Label: "Vertical", Value: "a\vb", Source: "env"},
		{Label: "FormFeed", Value: "a\fb", Source: "env"},
	}

	// When
	color.NoColor = false
	err := Detail(&b, fields)
	color.NoColor = true

	// Then
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()

	// One line per field, whatever the values contained.
	if got := len(strings.Split(strings.TrimRight(out, "\n"), "\n")); got != len(fields) {
		t.Errorf("lines = %d, want %d (one per field):\n%s", got, len(fields), out)
	}
	for _, bad := range []string{"line one\nline two", "a\tb", "a\vb", "a\fb"} {
		if strings.Contains(out, bad) {
			t.Errorf("structural whitespace %q survived into the row: %q", bad, out)
		}
	}
	if !strings.Contains(out, "line one line two") || !strings.Contains(out, "a b") {
		t.Errorf("output = %q, want the text preserved with spaces", out)
	}
	// Colouring still lands on the right columns.
	if !strings.Contains(out, "\x1b[2mconfig\x1b[0m") || !strings.Contains(out, "\x1b[2menv\x1b[0m") {
		t.Errorf("sources not dimmed exactly: %q", out)
	}
}

func TestSuccess(t *testing.T) {
	// Given
	var b bytes.Buffer

	// When
	Success(&b, "Created flag %s", "my-flag")

	// Then
	if got := b.String(); !strings.HasPrefix(got, "✓ ") || !strings.Contains(got, "Created flag my-flag") {
		t.Errorf("success = %q", got)
	}
}
