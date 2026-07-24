// Package bug marks errors the user should consider reporting: protocol
// violations, corrupt state, and infrastructure failures the CLI can neither
// recover from nor blame on the user's input. The command layer turns the
// mark into a report-an-issue hint (see internal/cmd hintFor).
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
