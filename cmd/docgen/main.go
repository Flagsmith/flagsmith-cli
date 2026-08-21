// Command docgen renders the CLI's own help into the website's markdown.
//
// Usage:
//
//	go run ./cmd/docgen -out website/content
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/cmd"
)

func main() {
	if err := run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet(filepath.Base(args[0]), flag.ContinueOnError)
	out := flags.String("out", "", "directory to write markdown pages to (required)")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if *out == "" {
		flags.Usage()
		return fmt.Errorf("-out not set")
	}

	// The directory is generated in full, so a deleted command leaves no page.
	if err := os.RemoveAll(*out); err != nil {
		return err
	}
	return write(cmd.Root(), *out)
}

// write renders cmd and its subcommands beneath dir.
func write(cmd *cobra.Command, dir string) error {
	// Otherwise the page ends with cobra's "Auto generated ... on <date>", which
	// makes the build non-reproducible.
	cmd.DisableAutoGenTag = true

	var page bytes.Buffer
	// Hextra renders the front matter title as the page heading, and cobra opens
	// with one of its own, so drop cobra's to avoid printing it twice.
	fmt.Fprintf(&page, "---\ntitle: %q\nlinkTitle: %q\ndescription: %q\n",
		cmd.CommandPath(), cmd.Name(), cmd.Short)
	if !cmd.HasParent() {
		fmt.Fprint(&page, "sidebar:\n  open: true\n")
	}
	fmt.Fprint(&page, "---\n\n")

	var body bytes.Buffer
	if err := doc.GenMarkdownCustom(cmd, &body, linker(cmd)); err != nil {
		return err
	}
	page.Write(dropHeading(body.Bytes()))

	// A command with subcommands owns a directory, so its page is that
	// directory's index; a leaf is a plain page beside its siblings.
	file := filepath.Join(dir, url(cmd.CommandPath())+".md")
	if cmd.HasAvailableSubCommands() {
		file = filepath.Join(dir, url(cmd.CommandPath()), "_index.md")
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(file, page.Bytes(), 0o644); err != nil {
		return err
	}

	for _, sub := range cmd.Commands() {
		if !sub.IsAvailableCommand() || sub.IsAdditionalHelpTopicCommand() {
			continue
		}
		if err := write(sub, dir); err != nil {
			return err
		}
	}
	return nil
}

// url is a command path's page relative to the site root, so
// "flagsmith flag update" is "flag/update" and "flagsmith" is ".".
func url(commandPath string) string {
	return filepath.Join(strings.Fields(commandPath)[1:]...)
}

// linker turns the flat filenames cobra writes in its "SEE ALSO" links
// (flagsmith_flag_update.md) into paths relative to the page being rendered, so
// they hold up under the /flagsmith-cli/ prefix a project site is served under.
// Command names contain no underscores, which TestCommandNamesAreURLSafe holds
// true, so splitting on them recovers the command path.
func linker(from *cobra.Command) func(string) string {
	return func(link string) string {
		to := strings.ReplaceAll(strings.TrimSuffix(link, ".md"), "_", " ")
		rel, err := filepath.Rel(url(from.CommandPath()), url(to))
		if err != nil {
			return link
		}
		return filepath.ToSlash(rel) + "/"
	}
}

// dropHeading removes the "## <command path>" line cobra opens with, and the
// blank line after it.
func dropHeading(md []byte) []byte {
	if !bytes.HasPrefix(md, []byte("## ")) {
		return md
	}
	_, rest, found := bytes.Cut(md, []byte("\n"))
	if !found {
		return md
	}
	return bytes.TrimLeft(rest, "\n")
}
