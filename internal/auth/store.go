package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
)

// Credentials is a stored credential for one Flagsmith instance.
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

// Token returns the secret used to authenticate requests.
func (c *Credentials) Token() string {
	if c.EffectiveKind() == KindMaster {
		return c.MasterKey
	}
	return c.AccessToken
}

const (
	keyringService = "flagsmith-cli"

	SourceKeychain = "keychain"
	SourceFile     = "file"
)

var (
	ErrNotLoggedIn = errors.New("not logged in — run `flagsmith login`")

	// ErrKeychainUnavailable means the OS keychain rejected a write. The
	// plaintext file store is an explicit opt-in, never a silent fallback.
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

// CredentialsFilePath is the plaintext fallback store used when the OS
// keychain is unavailable. It holds a JSON object keyed by instance URL.
func CredentialsFilePath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "flagsmith", "credentials.json"), nil
}

func readFileStore() (map[string]*Credentials, error) {
	path, err := CredentialsFilePath()
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]*Credentials{}, nil
		}
		return nil, err
	}
	store := map[string]*Credentials{}
	if err := json.Unmarshal(raw, &store); err != nil {
		return nil, fmt.Errorf("corrupt credentials file %s: %w", path, err)
	}
	return store, nil
}

func writeFileStore(store map[string]*Credentials) error {
	path, err := CredentialsFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(store)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// Save stores credentials for their instance in the given store:
// SourceKeychain, or SourceFile.
func Save(c *Credentials, store string) error {
	key := instanceKey(c.APIURL)
	switch store {
	case SourceKeychain:
		b, err := json.Marshal(c)
		if err != nil {
			return err
		}
		if err := keyring.Set(keyringService, key, string(b)); err != nil {
			return fmt.Errorf("%w: %v", ErrKeychainUnavailable, err)
		}
		return nil
	case SourceFile:
		entries, err := readFileStore()
		if err != nil {
			return err
		}
		entries[key] = c
		return writeFileStore(entries)
	default:
		return fmt.Errorf("unknown credential store %q", store)
	}
}

// Load returns the stored credentials for an instance and their source,
// or ErrNotLoggedIn.
func Load(apiURL string) (*Credentials, string, error) {
	key := instanceKey(apiURL)
	if raw, err := keyring.Get(keyringService, key); err == nil {
		c := &Credentials{}
		if err := json.Unmarshal([]byte(raw), c); err != nil {
			return nil, "", fmt.Errorf("corrupt credentials in keychain: %w", err)
		}
		return c, SourceKeychain, nil
	}
	store, err := readFileStore()
	if err != nil {
		return nil, "", err
	}
	if c, ok := store[key]; ok {
		return c, SourceFile, nil
	}
	return nil, "", ErrNotLoggedIn
}

// Delete removes stored credentials for an instance from all storage
// locations. The keychain removal is best-effort: a broken keychain
// shouldn't fail logout, and OAuth sessions are revoked server-side before
// local deletion anyway.
func Delete(apiURL string) error {
	key := instanceKey(apiURL)
	_ = keyring.Delete(keyringService, key)
	store, err := readFileStore()
	if err != nil {
		return err
	}
	if _, ok := store[key]; !ok {
		return nil
	}
	delete(store, key)
	return writeFileStore(store)
}
