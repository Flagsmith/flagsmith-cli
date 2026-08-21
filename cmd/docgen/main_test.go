package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/cmd"
)

func TestCommandNamesAreURLSafe(t *testing.T) {
	// Given
	root := cmd.Root()

	// When / Then
	var visit func(*cobra.Command)
	visit = func(c *cobra.Command) {
		assert.NotContains(t, c.Name(), "_", "an underscore breaks the links between pages")
		assert.NotContains(t, c.Name(), " ", "a space breaks the page's path")
		assert.NotContains(t, c.Name(), "/", "a slash breaks the page's path")

		for _, sub := range c.Commands() {
			visit(sub)
		}
	}
	visit(root)
}

func TestWriteMirrorsCommandTree(t *testing.T) {
	// Given
	dir := t.TempDir()

	// When
	require.NoError(t, write(cmd.Root(), dir))

	// Then
	// the root command's own page is the site's home page
	assert.FileExists(t, filepath.Join(dir, "_index.md"))
	// a command with subcommands has a directory
	assert.FileExists(t, filepath.Join(dir, "flag/_index.md"))
	// a leaf command is a page inside its parent's directory
	assert.FileExists(t, filepath.Join(dir, "flag/update.md"))
	// a hidden command is not published
	assert.NoFileExists(t, filepath.Join(dir, "flag/create.md"))
}

func TestWritePage(t *testing.T) {
	// Given
	dir := t.TempDir()
	require.NoError(t, write(cmd.Root(), dir))

	// When
	page, err := os.ReadFile(filepath.Join(dir, "flag/update.md"))
	require.NoError(t, err)
	got := string(page)

	// Then
	// the front matter titles the page with the full command path
	assert.Contains(t, got, `title: "flagsmith flag update"`)
	// the sidebar entry is the command's own name, unambiguous once nested.
	assert.Contains(t, got, `linkTitle: "update"`)
	// the description is the command's Short
	assert.Contains(t, got, `description: "Change a flag's state in the current environment"`)
	// the body is cobra's, taken from the command
	assert.Contains(t, got, "flagsmith flag update <feature> [flags]")
	// the link to the parent page is relative,
	assert.Contains(t, got, "](../)")
	// no link is left as cobra's flat filename
	assert.NotContains(t, got, "flagsmith_")
	// cobra's own heading is gone
	assert.NotContains(t, got, "## flagsmith flag update")
	// no generation date is stamped into the page
	assert.NotContains(t, got, "Auto generated")

	// Guard against the assertions above passing on an empty file.
	require.NotEmpty(t, strings.TrimSpace(got))
}
