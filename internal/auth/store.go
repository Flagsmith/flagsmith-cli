package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Flagsmith/flagsmith-cli/internal/bug"

	"github.com/zalando/go-keyring"
)

// Credentials is a stored login session for one Flagsmith instance.
type Credentials struct {
	Kind         Kind      `json:"kind,omitempty"` // empty means KindOAuth (back-compat)
	APIURL       string    `json:"api_url"`
	AccessToken  string    `json:"access_token,omitempty"`
	RefreshToken string    `json:"refresh_token,omitempty"`
	ExpiresAt    time.Time `json:"expires_at,omitzero"`
	MasterKey    string    `json:"master_key,omitempty"`
}

// EffectiveKind returns the credential kind, defaulting to KindOAuth for
// entries stored before kinds existed.
func (c *Credentials) EffectiveKind() Kind {
	if c.Kind == "" {
		return KindOAuth
	}
	return c.Kind
}

func (c *Credentials) Token() string {
	if c.EffectiveKind() == KindMaster {
		return c.MasterKey
	}
	return c.AccessToken
}

const (
	keyringService = "flagsmith-cli"

	// SourceKeychain labels credentials loaded from the OS keychain.
	SourceKeychain = "keychain"
)

var (
	// ErrNotLoggedIn means no credential is available for the instance.
	ErrNotLoggedIn = errors.New("not logged in")

	// ErrKeychainUnavailable means the OS keychain could not be used. Login
	// sessions are stored only in the keychain; when it is unavailable the
	// user must supply credentials via FLAGSMITH_API_KEY instead.
	ErrKeychainUnavailable = errors.New("OS keychain unavailable")
)

// KeychainAvailable probes the OS keychain with a write+delete round-trip.
func KeychainAvailable() bool {
	const probeKey = "storage-probe"
	if err := keyring.Set(keyringService, probeKey, "1"); err != nil {
		return false
	}
	_ = keyring.Delete(keyringService, probeKey)
	return true
}

// instanceKey normalizes an API URL into the per-instance storage key.
func instanceKey(apiURL string) string {
	return strings.TrimRight(apiURL, "/")
}

// Save stores a login session for its instance in the OS keychain.
func Save(c *Credentials) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := keyring.Set(keyringService, instanceKey(c.APIURL), string(b)); err != nil {
		return fmt.Errorf("%w: %v", ErrKeychainUnavailable, err)
	}
	return nil
}

// Load returns the stored session for an instance, or ErrNotLoggedIn when the
// keychain holds none. A keychain that cannot be read at all is reported as
// ErrKeychainUnavailable: telling that user to log in sends them through the
// whole browser flow only to fail at Save.
func Load(apiURL string) (*Credentials, error) {
	raw, err := keyring.Get(keyringService, instanceKey(apiURL))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil, ErrNotLoggedIn
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrKeychainUnavailable, err)
	}
	c := &Credentials{}
	if err := json.Unmarshal([]byte(raw), c); err != nil {
		return nil, bug.Mark(fmt.Errorf("corrupt credentials in keychain: %w", err))
	}
	return c, nil
}

// Delete removes the stored session for an instance. Missing is not an error.
func Delete(apiURL string) error {
	err := keyring.Delete(keyringService, instanceKey(apiURL))
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}
