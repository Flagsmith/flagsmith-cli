package cmd

import (
	"errors"
	"fmt"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/api"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/bug"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/config"
)

// A hint is an optional line rendered after an error message (before the usage
// block, when one is printed) to help the user recover — either a recovery
// command or a context link. Wrapping preserves the underlying error, so
// errors.Is/errors.As still see usageError and sentinels through a hintedError.

// Common hints reused across the CLI.
const (
	hintPricing = "This isn't available on your current plan — see https://flagsmith.com/pricing"
	hintQuota   = "Enterprise plans can raise this limit — get in touch: https://docs.flagsmith.com/support#getting-in-touch"
	hintLogin   = "Run `flagsmith login`, or set FLAGSMITH_API_KEY for non-interactive use."

	hintMasterKey        = "Set FLAGSMITH_API_KEY to use a Master API key instead."
	hintMasterKeyOrLogin = "Use a Master API key, or run `flagsmith login`."
	hintRelogin          = "Run `flagsmith login` to re-authenticate."
	hintAccessToken      = "For an OAuth access token, set FLAGSMITH_ACCESS_TOKEN instead."
	hintServerSideKey    = "Server-side keys are secrets — provide them via FLAGSMITH_ENVIRONMENT_KEY instead."

	hintEnvironmentKey = "Check FLAGSMITH_ENVIRONMENT_KEY, or the environment name/key passed with -e."
	hintSDKAPIURL      = "Check --sdk-api-url (or `sdkApiUrl`) — it must point at a Flagsmith SDK API."

	hintEnvironmentList  = "Run `flagsmith environment list` to see the environments in this project."
	hintProjectList      = "Run `flagsmith project list` to see the projects you can access."
	hintOrganisationList = "Run `flagsmith organisation list` to see the organisations you can access."
	hintFlagList         = "Run `flagsmith flag list` to see the flags in this environment."
	hintEvaluate         = "Run `flagsmith eval` to see the flags this environment resolves to."
	hintFeatureList      = "Run `flagsmith feature list` to see the features in this project."
	hintSegmentList      = "Run `flagsmith segment list` to see the segments in this project."

	hintWrongAccount = "Check `flagsmith auth status` — you may be logged in to the wrong account or instance."
	hintReportIssue  = "Think this shouldn't happen? Tell us: https://github.com/Flagsmith/flagsmith-cli/issues/new"
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
// present, otherwise one derived from a recognised condition, otherwise "". It
// is the single place mapping error conditions to recovery guidance.
func hintFor(err error) string {
	var h *hintedError
	if errors.As(err, &h) && h.hint != "" {
		return h.hint
	}
	switch {
	case errors.Is(err, auth.ErrNotLoggedIn):
		return hintLogin
	case errors.Is(err, auth.ErrRefreshFailed):
		return hintRelogin
	case errors.Is(err, auth.ErrKeychainUnavailable):
		return hintMasterKey
	case errors.Is(err, auth.ErrLegacyAuthtoken):
		return hintMasterKeyOrLogin
	case errors.Is(err, auth.ErrNotMasterKey):
		return hintAccessToken
	case errors.Is(err, auth.ErrServerSideKey), errors.Is(err, config.ErrServerSideKey):
		return hintServerSideKey
	case errors.Is(err, api.ErrQuotaExceeded):
		return hintQuota
	case errors.Is(err, api.ErrPlanGated):
		return hintPricing
	case errors.Is(err, api.ErrWorkflowGated):
		return docsHint("advanced-use/change-requests")
	// Last, so any condition with a specific recovery wins over "report it".
	case errors.Is(err, bug.ErrUnexpected):
		return hintReportIssue
	}
	return ""
}
