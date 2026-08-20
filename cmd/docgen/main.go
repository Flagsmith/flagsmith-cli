// Command docgen renders the CLI's own help into markdown for the website.
//
// Pages are cobra's own markdown output, so the published reference cannot
// drift from the binary: every page is the command's Short, Long, Example and
// flags as the terminal would print them. Hidden commands and hidden flags are
// skipped.
//
// The command tree is mirrored as a directory tree — `flagsmith flag update`
// becomes flag/update.md, served at /flag/update/ — so the site navigation
// nests the way the CLI does. cobra's own tree walker is not used because it
// flattens everything into one directory, which leaves a sidebar of six
// commands all called "create".
//
// The output is the whole site content: the root command's page is the home
// page, so there is nowhere for a hand-written description of the CLI to drift
// out of step with the CLI.
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
	"path"
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

	root := cmd.Root()
	// Every page ends with cobra's "Auto generated ... on <date>" footer
	// otherwise, which makes the build non-reproducible. cobra only inherits
	// this from a parent while rendering its "SEE ALSO" block, so set it on
	// each command rather than trusting it to propagate from the root.
	forEach(root, func(c *cobra.Command) { c.DisableAutoGenTag = true })

	// The whole directory is generated, so clear it: a renamed or deleted
	// command must not leave an orphan page behind.
	if err := os.RemoveAll(*out); err != nil {
		return err
	}
	return write(root, newSite(root), *out)
}

// forEach applies fn to cmd and every command beneath it, skipping the ones
// cobra would not document.
func forEach(cmd *cobra.Command, fn func(*cobra.Command)) {
	fn(cmd)
	for _, sub := range cmd.Commands() {
		if !sub.IsAvailableCommand() || sub.IsAdditionalHelpTopicCommand() {
			continue
		}
		forEach(sub, fn)
	}
}

// site maps each documented command to where its page lives, and remembers the
// link names cobra will ask about while rendering cross-references.
type site struct {
	// url is the served path of a command's page, e.g. "flag/update", relative
	// to the site root.
	url map[*cobra.Command]string
	// byLink resolves the "flagsmith_flag_update.md" names cobra emits in its
	// SEE ALSO block back to the command they point at.
	byLink map[string]*cobra.Command
}

func newSite(root *cobra.Command) *site {
	s := &site{url: map[*cobra.Command]string{}, byLink: map[string]*cobra.Command{}}
	forEach(root, func(c *cobra.Command) {
		// path.Join returns "" for the root command; "." keeps it a valid path
		// for filepath.Rel when links are computed.
		s.url[c] = path.Join(".", strings.Join(segments(c), "/"))
		s.byLink[strings.ReplaceAll(c.CommandPath(), " ", "_")+".md"] = c
	})
	return s
}

// segments is a command's path below the root, so `flagsmith flag update`
// yields [flag update] and the root itself yields nothing.
func segments(cmd *cobra.Command) []string {
	return strings.Fields(cmd.CommandPath())[1:]
}

// write renders cmd and its subcommands beneath dir.
func write(cmd *cobra.Command, s *site, dir string) error {
	page, err := render(cmd, s)
	if err != nil {
		return err
	}

	// A command with subcommands owns a directory, so its own page has to be
	// that directory's index; a leaf is a plain page beside its siblings.
	file := filepath.Join(dir, filepath.Join(segments(cmd)...)+".md")
	if cmd.HasAvailableSubCommands() {
		file = filepath.Join(dir, filepath.Join(segments(cmd)...), "_index.md")
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(file, page, 0o644); err != nil {
		return err
	}

	for _, sub := range cmd.Commands() {
		if !sub.IsAvailableCommand() || sub.IsAdditionalHelpTopicCommand() {
			continue
		}
		if err := write(sub, s, dir); err != nil {
			return err
		}
	}
	return nil
}

// render produces one page: Hugo front matter, then cobra's markdown.
func render(cmd *cobra.Command, s *site) ([]byte, error) {
	var body bytes.Buffer
	if err := doc.GenMarkdownCustom(cmd, &body, s.linker(cmd)); err != nil {
		return nil, err
	}

	var page bytes.Buffer
	// Hextra renders the title as the page's heading, and cobra opens with the
	// command path as a heading too. Keep the front matter one and drop
	// cobra's, so the command name is not printed twice.
	fmt.Fprintf(&page, "---\ntitle: %s\nlinkTitle: %s\ndescription: %s\n",
		yamlString(cmd.CommandPath()), yamlString(cmd.Name()), yamlString(cmd.Short))
	if !cmd.HasParent() {
		// Expand the top of the sidebar on arrival.
		fmt.Fprint(&page, "sidebar:\n  open: true\n")
	}
	fmt.Fprint(&page, "---\n\n")
	page.Write(dropHeading(body.Bytes()))
	return page.Bytes(), nil
}

// linker resolves cobra's cross-reference filenames into links relative to the
// page being rendered, so they survive being served under a project-pages
// subpath such as /flagsmith-cli/.
func (s *site) linker(from *cobra.Command) func(string) string {
	return func(link string) string {
		to, ok := s.byLink[link]
		if !ok {
			return link
		}
		rel, err := filepath.Rel(s.url[from], s.url[to])
		if err != nil {
			return link
		}
		return filepath.ToSlash(rel) + "/"
	}
}

// dropHeading removes the leading "## <command path>" line cobra writes, along
// with the blank line after it.
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

// yamlString quotes a value for a front matter scalar, since a Short can
// contain a colon or a quote.
func yamlString(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}
