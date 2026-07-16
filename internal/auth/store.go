package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"
)

// Credentials is the stored OAuth session for one Flagsmith instance.
type Credentials struct {
	APIURL       string    `json:"api_url"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
}

const (
	keyringService = "flagsmith-cli"
	keyringUser    = "default"

	SourceKeychain = "keychain"
	SourceFile     = "file"
)

var ErrNotLoggedIn = errors.New("not logged in — run `flagsmith login`")

func credentialsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "flagsmith", "credentials.json"), nil
}

// Save stores credentials in the OS keychain, falling back to a 0600 file.
// It returns where they were stored (SourceKeychain or SourceFile).
func Save(c *Credentials) (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	if err := keyring.Set(keyringService, keyringUser, string(b)); err == nil {
		return SourceKeychain, nil
	}
	path, err := credentialsPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	return SourceFile, nil
}

// Load returns stored credentials and their source, or ErrNotLoggedIn.
func Load() (*Credentials, string, error) {
	if raw, err := keyring.Get(keyringService, keyringUser); err == nil {
		c := &Credentials{}
		if err := json.Unmarshal([]byte(raw), c); err != nil {
			return nil, "", fmt.Errorf("corrupt credentials in keychain: %w", err)
		}
		return c, SourceKeychain, nil
	}
	path, err := credentialsPath()
	if err != nil {
		return nil, "", err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", ErrNotLoggedIn
		}
		return nil, "", err
	}
	c := &Credentials{}
	if err := json.Unmarshal(raw, c); err != nil {
		return nil, "", fmt.Errorf("corrupt credentials file %s: %w", path, err)
	}
	return c, SourceFile, nil
}

// Delete removes stored credentials from all storage locations.
func Delete() error {
	kerr := keyring.Delete(keyringService, keyringUser)
	if errors.Is(kerr, keyring.ErrNotFound) {
		kerr = nil
	}
	path, perr := credentialsPath()
	if perr == nil {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			perr = err
		} else {
			perr = nil
		}
	}
	if kerr != nil {
		return kerr
	}
	return perr
}
