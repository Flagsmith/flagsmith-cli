// Package prompt implements the interactive prompts on charmbracelet/huh.
package prompt

import (
	"bufio"
	"errors"
	"io"

	"github.com/charmbracelet/huh"
)

// IO carries a prompt's streams. All prompts go to ErrOut (stderr).
//
// RawTTY selects the full-terminal UI and must only be true when stdin is a real
// terminal; otherwise huh runs in accessible mode reading In and writing ErrOut.
// On EOF, accessible prompts terminate with their default value rather than hang.
type IO struct {
	In     *bufio.Reader
	ErrOut io.Writer
	RawTTY bool
}

// unbuffered reads at most one byte per Read call. huh's accessible mode
// wraps its input in a fresh bufio.Scanner per prompt; a buffering source
// would let one prompt's scanner swallow the input meant for the next.
type unbuffered struct{ r io.Reader }

func (u unbuffered) Read(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return u.r.Read(p)
}

func run(streams IO, field huh.Field) error {
	form := huh.NewForm(huh.NewGroup(field)).
		WithAccessible(!streams.RawTTY)
	if !streams.RawTTY {
		form = form.WithInput(unbuffered{streams.In}).WithOutput(streams.ErrOut)
	}
	err := form.Run()
	if errors.Is(err, huh.ErrUserAborted) {
		return errors.New("cancelled")
	}
	return err
}

// Select asks the user to pick an option and returns its index.
func Select(streams IO, label string, options []string, def int) (int, error) {
	idx := def
	opts := make([]huh.Option[int], len(options))
	for i, option := range options {
		opts[i] = huh.NewOption(option, i)
	}
	field := huh.NewSelect[int]().Title(label).Options(opts...).Value(&idx)
	if err := run(streams, field); err != nil {
		return 0, err
	}
	return idx, nil
}

// Text asks for a line of input with a default.
func Text(streams IO, label, def string) (string, error) {
	value := def
	field := huh.NewInput().Title(label).Value(&value)
	if err := run(streams, field); err != nil {
		return "", err
	}
	return value, nil
}

// Confirm asks a yes/no question, defaulting to no.
func Confirm(streams IO, label string) (bool, error) {
	ok := false
	field := huh.NewConfirm().Title(label).Value(&ok)
	if err := run(streams, field); err != nil {
		return false, err
	}
	return ok, nil
}
