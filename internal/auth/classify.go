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
	// Bare access token from the environment.
	KindBearer Kind = "bearer"
)

var legacyAuthtokenPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

// ClassifyAPIKey maps a FLAGSMITH_API_KEY value to its credential kind.
func ClassifyAPIKey(value string) (Kind, error) {
	switch {
	case strings.HasPrefix(value, "ser."):
		return "", errors.New(
			"FLAGSMITH_API_KEY contains a server-side environment key. Set FLAGSMITH_ENVIRONMENT_KEY instead")
	case legacyAuthtokenPattern.MatchString(value):
		return "", errors.New(
			"legacy user authtokens are not supported. Use a Master API key or `flagsmith login`")
	case strings.Contains(value, "."):
		return KindMaster, nil
	default:
		return KindBearer, nil
	}
}
