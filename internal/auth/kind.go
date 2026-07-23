package auth

import (
	"errors"
	"regexp"
	"strings"
)

// Kind is the credential type, which determines the Authorization scheme.
type Kind string

const (
	// Browser-login session: Bearer access token + refresh token.
	KindOAuth Kind = "oauth"
	// Organisation Master API key: `Api-Key {prefix}.{secret}`.
	KindMaster Kind = "master"
	// OAuth-style access token from FLAGSMITH_ACCESS_TOKEN (OIDC-exchanged in CI): `Bearer {token}`.
	KindBearer Kind = "bearer"
)

var legacyAuthtokenPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// ValidateMasterKey checks that a FLAGSMITH_API_KEY value is a Master API key.
// Each Admin API env var maps to exactly one credential kind, so the CLI never
// guesses a scheme from token shape; this validation only turns the common
// paste-mistakes into actionable errors instead of a silently rejected request.
func ValidateMasterKey(value string) error {
	switch {
	case strings.HasPrefix(value, "ser."):
		return errors.New(
			"FLAGSMITH_API_KEY contains a server-side environment key. Set FLAGSMITH_ENVIRONMENT_KEY instead")
	case legacyAuthtokenPattern.MatchString(value):
		return errors.New(
			"FLAGSMITH_API_KEY contains a legacy user authtoken, which is not supported. Use a Master API key or `flagsmith login`")
	case !strings.Contains(value, "."):
		return errors.New(
			"FLAGSMITH_API_KEY is not a Master API key (expected {prefix}.{secret}). For an OAuth access token, set FLAGSMITH_ACCESS_TOKEN instead")
	default:
		return nil
	}
}
