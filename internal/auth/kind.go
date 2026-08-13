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

// ValidateMasterKey's rejections. Each reads as the predicate of a sentence
// whose subject is the variable the value came from, which only the caller
// knows: the Master API key variable is host-scoped, so naming it here would
// name the wrong one for every self-hosted instance.
var (
	ErrServerSideKey   = errors.New("holds a server-side environment key")
	ErrLegacyAuthtoken = errors.New("holds a legacy user authtoken, which is not supported")
	ErrNotMasterKey    = errors.New("is not a Master API key (expected {prefix}.{secret})")
)

// ValidateMasterKey checks that a credential value is a Master API key. Each
// Admin API env var maps to exactly one credential kind, so the scheme is
// never guessed from token shape; this only turns common paste-mistakes into
// actionable errors instead of a silently rejected request.
func ValidateMasterKey(value string) error {
	switch {
	case strings.HasPrefix(value, "ser."):
		return ErrServerSideKey
	case legacyAuthtokenPattern.MatchString(value):
		return ErrLegacyAuthtoken
	case !strings.Contains(value, "."):
		return ErrNotMasterKey
	default:
		return nil
	}
}
