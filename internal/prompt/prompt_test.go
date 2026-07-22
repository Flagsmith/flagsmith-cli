package prompt

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func testIO(input string) (IO, *bytes.Buffer) {
	out := &bytes.Buffer{}
	return IO{In: bufio.NewReader(strings.NewReader(input)), ErrOut: out, RawTTY: false}, out
}

func TestSelectAccessible(t *testing.T) {
	// Given
	io, out := testIO("2\n")

	// When
	idx, err := Select(io, "Project", []string{"a", "b", "c"}, 0)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if idx != 1 {
		t.Errorf("idx = %d, want 1", idx)
	}
	for _, want := range []string{"Project", "1. a", "2. b", "Enter a number between 1 and 3"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output = %q, want it to contain %q", out.String(), want)
		}
	}
}

func TestSelectRepromptsOnInvalidChoice(t *testing.T) {
	// Given
	io, out := testIO("9\nx\n2\n")

	// When
	idx, err := Select(io, "Project", []string{"a", "b", "c"}, 0)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if idx != 1 {
		t.Errorf("idx = %d, want 1", idx)
	}
	if got := strings.Count(out.String(), "must be a number between 1 and 3"); got != 2 {
		t.Errorf("output = %q, want two re-prompts", out.String())
	}
}

func TestSelectDefaultsOnEmpty(t *testing.T) {
	// Given
	io, _ := testIO("\n")

	// When
	idx, err := Select(io, "Project", []string{"a", "b"}, 1)

	// Then
	if err != nil || idx != 1 {
		t.Errorf("(idx, err) = (%d, %v), want the default", idx, err)
	}
}

func TestSelectEOFFallsBackToDefaultWithoutHanging(t *testing.T) {
	// Given
	io, _ := testIO("")

	// When — EOF must terminate, never spin (02: never a hang)
	idx, err := Select(io, "Project", []string{"a", "b"}, 1)

	// Then
	if err != nil || idx != 1 {
		t.Errorf("(idx, err) = (%d, %v), want the default on EOF", idx, err)
	}
}

func TestConfirmReprompts(t *testing.T) {
	// Given
	io, out := testIO("maybe\ny\n")

	// When
	ok, err := Confirm(io, "Write changes?")

	// Then
	if err != nil || !ok {
		t.Errorf("(ok, err) = (%v, %v), want yes after a re-prompt", ok, err)
	}
	if got := strings.Count(out.String(), "y/N"); got < 2 {
		t.Errorf("output = %q, want a re-prompt", out.String())
	}
}

func TestConfirmDefaultsToNo(t *testing.T) {
	// Given
	io, _ := testIO("\n")

	// When
	ok, err := Confirm(io, "Write changes?")

	// Then
	if err != nil || ok {
		t.Errorf("(ok, err) = (%v, %v), want the No default", ok, err)
	}
}

func TestTextDefault(t *testing.T) {
	// Given
	io, _ := testIO("\n")

	// When
	got, err := Text(io, "Project name", "acme-web")

	// Then
	if err != nil || got != "acme-web" {
		t.Errorf("(got, err) = (%q, %v), want the default", got, err)
	}
}

func TestSequentialPromptsShareInput(t *testing.T) {
	// Given — one input stream feeding two consecutive prompts
	io, _ := testIO("2\ny\n")

	// When
	idx, err := Select(io, "Project", []string{"a", "b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := Confirm(io, "Write changes?")

	// Then — the select must not swallow the confirm's input
	if err != nil {
		t.Fatal(err)
	}
	if idx != 1 || !ok {
		t.Errorf("(idx, ok) = (%d, %v), want (1, true)", idx, ok)
	}
}

func TestTextValue(t *testing.T) {
	// Given
	io, _ := testIO("custom-name\n")

	// When
	got, err := Text(io, "Project name", "acme-web")

	// Then
	if err != nil || got != "custom-name" {
		t.Errorf("(got, err) = (%q, %v)", got, err)
	}
}
