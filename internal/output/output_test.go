package output

import (
	"bytes"
	"encoding/json"
	"io"
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

	// Then — a bare object matching the data shape
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

		// When — JQ set, JSON not explicitly on
		err := Render(&b, sample{7, "beta"}, Options{JQ: ".name"}, func(io.Writer) error {
			t.Fatal("human renderer must not run when --jq is set")
			return nil
		})

		// Then — raw string, not quoted JSON
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

		// Then — two JSON objects, one per line
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

	// Then — headers and both rows present, columns aligned
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

func TestDetail(t *testing.T) {
	// Given
	var b bytes.Buffer

	// When
	err := Detail(&b, []Field{{"Name", "my-flag"}, {"Enabled", "true"}})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if !strings.Contains(out, "Name") || !strings.Contains(out, "my-flag") {
		t.Errorf("detail = %q", out)
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
