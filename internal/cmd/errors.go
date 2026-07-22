package cmd

import (
	"errors"
	"fmt"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/auth"
)

// A hint is an optional line rendered after an error message (before the usage
// block, when one is printed) to help the user recover — either a recovery
// command or a context link. See docs/design/02-output-and-interactivity.md §4.
//
// Hints reach the surface two ways:
//   - explicitly, by wrapping any error with withHint / hintf at the call site;
//   - automatically, when the error is (or wraps) a known condition that hintFor
//     recognises — e.g. api.ErrPlanGated always suggests the pricing page, with
//     no work at the call site.
//
// Wrapping preserves the underlying error, so errors.Is/errors.As still see
// usageError, sentinels, etc. through a hintedError.

// Common hints reused across the CLI.
const (
	hintPricing = "This isn't available on your current plan — see https://flagsmith.com/pricing"
	hintLogin   = "Run `flagsmith login`, or set FLAGSMITH_API_KEY for non-interactive use."
)

// docsHint points at a page under docs.flagsmith.com.
func docsHint(path string) string {
	return "See https://docs.flagsmith.com/" + path
}

// hintedError carries a hint alongside an error.
type hintedError struct {
	err  error
	hint string
}

func (e *hintedError) Error() string { return e.err.Error() }
func (e *hintedError) Unwrap() error { return e.err }

// withHint attaches hint to err. It is nil-safe and a no-op for an empty hint,
// so it composes freely: return withHint(doThing(), "Try: flagsmith login").
func withHint(err error, hint string) error {
	if err == nil || hint == "" {
		return err
	}
	return &hintedError{err: err, hint: hint}
}

// hintf attaches a formatted hint to err.
func hintf(err error, format string, a ...any) error {
	return withHint(err, fmt.Sprintf(format, a...))
}

// hintFor returns the hint to display for err: an explicitly attached hint if
// present, otherwise one derived from a recognised condition, otherwise "".
// This is the single place that maps error conditions to recovery guidance, so
// any command surfacing such an error gets the hint for free.
func hintFor(err error) string {
	var h *hintedError
	if errors.As(err, &h) && h.hint != "" {
		return h.hint
	}
	switch {
	case errors.Is(err, auth.ErrNotLoggedIn):
		return hintLogin
	case errors.Is(err, api.ErrPlanGated):
		return hintPricing
	case errors.Is(err, api.ErrWorkflowGated):
		return docsHint("advanced-use/change-requests")
	}
	return ""
}
