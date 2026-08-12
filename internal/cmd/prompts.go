package cmd

import (
	"bufio"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/prompt"
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

// noInput reports the non-interactive switch: --no-input or FLAGSMITH_NO_INPUT.
// It is a liveness guarantee (never block on a human), orthogonal to --yes
// (authorization).
func noInput() bool {
	return noInputFlag || envBool("FLAGSMITH_NO_INPUT")
}

// interactive reports whether prompting is allowed: a TTY on stdin and no
// non-interactive switch. --yes does not suppress prompting — it only
// pre-answers confirmations.
func interactive() bool {
	return stdinIsTTY() && !noInput()
}

// promptIn buffers the command's stdin across consecutive prompts.
var promptIn *bufio.Reader

func initPrompts(cmd *cobra.Command) {
	promptIn = bufio.NewReader(cmd.InOrStdin())
}

func promptIO(cmd *cobra.Command) prompt.IO {
	// A prompt takes over the terminal, and in raw mode writes to it directly
	// rather than through cobra, so it cannot be trusted to clear a standing flag
	// on its own.
	releaseLine()
	return prompt.IO{In: promptIn, ErrOut: cmd.ErrOrStderr(), RawTTY: rawTerminal()}
}

// The prompt helpers take the flag that supplies the same value without a TTY,
// and self-guard: called non-interactively they return a usage error naming
// that flag rather than hanging.

func selectPrompt(cmd *cobra.Command, flag, label string, options []string, def int) (int, error) {
	if !interactive() {
		return 0, usageErrorf("--%s is required without an interactive terminal", flag)
	}
	idx, err := prompt.Select(promptIO(cmd), label, options, def)
	refreshCommandTimeout(cmd)
	return idx, err
}

func textPrompt(cmd *cobra.Command, flag, label, def string) (string, error) {
	if !interactive() {
		return "", usageErrorf("--%s is required without an interactive terminal", flag)
	}
	s, err := prompt.Text(promptIO(cmd), label, def)
	refreshCommandTimeout(cmd)
	return s, err
}

// confirmOrYes resolves a yes/no confirmation. --yes authorizes it; otherwise
// an interactive TTY prompts; otherwise (--no-input, FLAGSMITH_NO_INPUT, or no
// TTY) it is a usage error naming --yes. Non-interactive execution
// never authorizes on its own: --no-input is a liveness switch, not consent.
func confirmOrYes(cmd *cobra.Command, label string) (bool, error) {
	if yesFlag {
		return true, nil
	}
	if !interactive() {
		return false, usageErrorf("pass --yes to confirm %q without an interactive terminal", label)
	}
	ok, err := prompt.Confirm(promptIO(cmd), label)
	refreshCommandTimeout(cmd)
	return ok, err
}

// confirmed resolves a confirmation and, on decline, prints the standard abort
// line: "Aborted; nothing <outcome>." — outcome names what was left untouched
// (deleted / changed / written). A decline is ok=false with a nil error.
func confirmed(cmd *cobra.Command, prompt, outcome string) (bool, error) {
	ok, err := confirmOrYes(cmd, prompt)
	if err != nil {
		return false, err
	}
	if !ok {
		fmt.Fprintf(cmd.ErrOrStderr(), "Aborted; nothing %s.\n", outcome)
	}
	return ok, nil
}
