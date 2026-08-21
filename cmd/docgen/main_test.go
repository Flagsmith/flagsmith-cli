package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/cmd"
)

// Links between pages are recovered from the underscore-joined filenames cobra
// writes, so a command name containing one would produce a broken link.
func TestCommandNamesAreURLSafe(t *testing.T) {
	var check func(*cobra.Command)
	check = func(c *cobra.Command) {
		if strings.ContainsAny(c.Name(), "_ ") {
			t.Errorf("command %q: name must not contain an underscore or space", c.CommandPath())
		}
		for _, sub := range c.Commands() {
			check(sub)
		}
	}
	check(cmd.Root())
}

func TestWriteMirrorsCommandTree(t *testing.T) {
	dir := t.TempDir()
	if err := write(cmd.Root(), dir); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"_index.md", "flag/_index.md", "flag/update.md"} {
		if _, err := os.Stat(filepath.Join(dir, want)); err != nil {
			t.Errorf("%s: %v", want, err)
		}
	}
	// A hidden command must not be published.
	if _, err := os.Stat(filepath.Join(dir, "flag/create.md")); err == nil {
		t.Error("flag/create.md: hidden command should not have a page")
	}

	page, err := os.ReadFile(filepath.Join(dir, "flag/update.md"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(page)
	for _, want := range []string{
		`title: "flagsmith flag update"`, // Hextra renders the page heading from it
		"](../)",                         // link to the parent page, not a flat filename
	} {
		if !strings.Contains(got, want) {
			t.Errorf("page is missing %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{
		"Auto generated",           // non-reproducible build
		"## flagsmith flag update", // duplicates the front matter title
		"flagsmith_flag_update.md", // cobra's flat filenames
	} {
		if strings.Contains(got, unwanted) {
			t.Errorf("page should not contain %q:\n%s", unwanted, got)
		}
	}
}
