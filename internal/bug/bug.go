// Package bug marks errors the user should consider reporting: protocol
// violations, corrupt state, infrastructure failures — never bad user input.
package bug

import "errors"

// ErrUnexpected is the marker Mark attaches; test for it with errors.Is.
var ErrUnexpected = errors.New("unexpected failure")

type marked struct{ err error }

func (m *marked) Error() string { return m.err.Error() }

// Unwrap exposes both the original chain and the marker, so errors.Is sees
// wrapped sentinels through the mark and the mark itself, while Error()
// stays untouched.
func (m *marked) Unwrap() []error { return []error{m.err, ErrUnexpected} }

// Mark tags err as unexpected without changing its message. It is nil-safe,
// so it composes freely: return bug.Mark(doThing()).
func Mark(err error) error {
	if err == nil {
		return nil
	}
	return &marked{err: err}
}
