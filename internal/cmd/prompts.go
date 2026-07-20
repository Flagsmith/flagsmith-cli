package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// stdinIsTTY is a seam for tests; prompts require a real terminal.
var stdinIsTTY = func() bool {
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

func readPromptLine() (string, error) {
	line, err := promptIn.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// selectPrompt shows numbered options and returns the chosen index.
func selectPrompt(cmd *cobra.Command, label string, options []string, def int) (int, error) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "? %s\n", label)
	for i, option := range options {
		fmt.Fprintf(out, "  %d) %s\n", i+1, option)
	}
	fmt.Fprintf(out, "  Choice [%d]: ", def+1)
	line, err := readPromptLine()
	if err != nil {
		return 0, err
	}
	if line == "" {
		return def, nil
	}
	n, err := strconv.Atoi(line)
	if err != nil || n < 1 || n > len(options) {
		return 0, fmt.Errorf("invalid choice %q — pick 1-%d", line, len(options))
	}
	return n - 1, nil
}

// textPrompt asks for a line of input with a default.
func textPrompt(cmd *cobra.Command, label, def string) (string, error) {
	fmt.Fprintf(cmd.OutOrStdout(), "? %s [%s]: ", label, def)
	line, err := readPromptLine()
	if err != nil {
		return "", err
	}
	if line == "" {
		return def, nil
	}
	return line, nil
}

// confirmPrompt asks a y/N question, defaulting to no.
func confirmPrompt(cmd *cobra.Command, label string) (bool, error) {
	fmt.Fprintf(cmd.OutOrStdout(), "? %s (y/N): ", label)
	line, err := readPromptLine()
	if err != nil {
		return false, err
	}
	return strings.EqualFold(line, "y") || strings.EqualFold(line, "yes"), nil
}
