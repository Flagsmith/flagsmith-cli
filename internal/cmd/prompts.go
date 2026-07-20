package cmd

import (
	"bufio"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Flagsmith/flagsmith-cli/internal/prompt"
)

// stdinIsTTY gates whether prompting is allowed at all; tests stub it.
var stdinIsTTY = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// rawTerminal gates the arrow-key selector, which puts the real stdin in
// raw mode — never stubbed, so tests exercise the line-based fallback.
var rawTerminal = func() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// interactive reports whether prompting is allowed: a TTY on stdin, no
// --yes/--no-input, no FLAGSMITH_NO_INPUT.
func interactive() bool {
	return stdinIsTTY() && !yesFlag && os.Getenv("FLAGSMITH_NO_INPUT") == ""
}

// promptIn buffers the command's stdin across consecutive prompts.
var promptIn *bufio.Reader

func initPrompts(cmd *cobra.Command) {
	promptIn = bufio.NewReader(cmd.InOrStdin())
}

func promptIO(cmd *cobra.Command) prompt.IO {
	return prompt.IO{In: promptIn, Out: cmd.OutOrStdout(), RawTTY: rawTerminal()}
}

func selectPrompt(cmd *cobra.Command, label string, options []string, def int) (int, error) {
	return prompt.Select(promptIO(cmd), label, options, def)
}

func textPrompt(cmd *cobra.Command, label, def string) (string, error) {
	return prompt.Text(promptIO(cmd), label, def)
}

func confirmPrompt(cmd *cobra.Command, label string) (bool, error) {
	return prompt.Confirm(promptIO(cmd), label)
}
