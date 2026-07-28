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

// ValidateMasterKey's rejections.
var (
	ErrServerSideKey   = errors.New("FLAGSMITH_API_KEY contains a server-side environment key")
	ErrLegacyAuthtoken = errors.New("FLAGSMITH_API_KEY contains a legacy user authtoken, which is not supported")
	ErrNotMasterKey    = errors.New("FLAGSMITH_API_KEY is not a Master API key (expected {prefix}.{secret})")
)

// ValidateMasterKey checks that a FLAGSMITH_API_KEY value is a Master API key.
// Each Admin API env var maps to exactly one credential kind, so the scheme is
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
