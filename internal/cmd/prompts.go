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

// The prompt helpers take the flag that supplies the same value without a
// TTY, and self-guard: called non-interactively they return a usage error
// (exit 2) naming that flag rather than hanging. This structurally links
// every prompt to a flag — a prompt cannot be added without naming one.

func selectPrompt(cmd *cobra.Command, flag, label string, options []string, def int) (int, error) {
	if !interactive() {
		return 0, usageErrorf("--%s is required without an interactive terminal", flag)
	}
	return prompt.Select(promptIO(cmd), label, options, def)
}

func textPrompt(cmd *cobra.Command, flag, label, def string) (string, error) {
	if !interactive() {
		return "", usageErrorf("--%s is required without an interactive terminal", flag)
	}
	return prompt.Text(promptIO(cmd), label, def)
}

// confirmOrYes resolves a yes/no confirmation: --yes/--no-input answers it
// affirmatively; without a TTY and without --yes it is a usage error (exit
// 2) naming --yes; otherwise it prompts.
func confirmOrYes(cmd *cobra.Command, label string) (bool, error) {
	if yesFlag || os.Getenv("FLAGSMITH_NO_INPUT") != "" {
		return true, nil
	}
	if !stdinIsTTY() {
		return false, usageErrorf("pass --yes to confirm %q without an interactive terminal", label)
	}
	return prompt.Confirm(promptIO(cmd), label)
}
