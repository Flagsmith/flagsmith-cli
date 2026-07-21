package auth

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	saasURL = "https://api.flagsmith.com"
	selfURL = "https://flagsmith.acme.internal"
)

// isolateStorage points the keychain at go-keyring's in-memory mock and
// gives HOME a temp directory.
func isolateStorage(t *testing.T) {
	t.Helper()
	keyring.MockInit()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tmp, ".config"))
	t.Setenv("AppData", tmp)
}

func oauthCredentials(apiURL string) *Credentials {
	return &Credentials{
		Kind:         KindOAuth,
		APIURL:       apiURL,
		AccessToken:  "access-" + apiURL,
		RefreshToken: "refresh-" + apiURL,
		ExpiresAt:    time.Now().Add(15 * time.Minute).Truncate(time.Second),
	}
}

func assertCredentialsEqual(t *testing.T, got, want *Credentials) {
	t.Helper()
	if got.Kind != want.Kind ||
		got.APIURL != want.APIURL ||
		got.AccessToken != want.AccessToken ||
		got.RefreshToken != want.RefreshToken ||
		got.MasterKey != want.MasterKey ||
		!got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("credentials round-trip mismatch:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestStoreKeychainRoundTrip(t *testing.T) {
	// Given
	isolateStorage(t)
	want := oauthCredentials(saasURL)

	// When
	if err := Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := Load(saasURL)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	assertCredentialsEqual(t, got, want)

	// When
	if err := Delete(saasURL); err != nil {
		t.Fatal(err)
	}

	// Then
	if _, err := Load(saasURL); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Load after Delete = %v, want ErrNotLoggedIn", err)
	}
}

func TestStoreKeysByInstance(t *testing.T) {
	// Given
	isolateStorage(t)
	saas := oauthCredentials(saasURL)
	self := oauthCredentials(selfURL)
	if err := Save(saas); err != nil {
		t.Fatal(err)
	}
	if err := Save(self); err != nil {
		t.Fatal(err)
	}

	// When / Then — each instance is stored independently
	gotSaas, err := Load(saasURL)
	if err != nil {
		t.Fatal(err)
	}
	assertCredentialsEqual(t, gotSaas, saas)

	if err := Delete(selfURL); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(selfURL); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Load(self) after Delete(self) = %v, want ErrNotLoggedIn", err)
	}
	if _, err := Load(saasURL); err != nil {
		t.Errorf("Load(saas) after Delete(self) = %v, want untouched", err)
	}
}

func TestStoreNormalizesInstanceURL(t *testing.T) {
	// Given
	isolateStorage(t)
	want := oauthCredentials(saasURL)
	if err := Save(want); err != nil {
		t.Fatal(err)
	}

	// When — a trailing slash resolves to the same entry
	got, err := Load(saasURL + "/")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	assertCredentialsEqual(t, got, want)
}

func TestSaveFailsWithoutKeychain(t *testing.T) {
	// Given
	isolateStorage(t)
	keyring.MockInitWithError(errors.New("keychain locked"))

	// When / Then — no silent fallback; login must fail closed
	if err := Save(oauthCredentials(saasURL)); !errors.Is(err, ErrKeychainUnavailable) {
		t.Errorf("err = %v, want ErrKeychainUnavailable", err)
	}
}

func TestKeychainAvailable(t *testing.T) {
	t.Run("working keychain", func(t *testing.T) {
		// Given
		isolateStorage(t)

		// When / Then
		if !KeychainAvailable() {
			t.Error("KeychainAvailable = false with a working keychain")
		}
	})

	t.Run("broken keychain", func(t *testing.T) {
		// Given
		isolateStorage(t)
		keyring.MockInitWithError(errors.New("keychain locked"))

		// When / Then
		if KeychainAvailable() {
			t.Error("KeychainAvailable = true with a broken keychain")
		}
	})
}

func TestLoadNotLoggedIn(t *testing.T) {
	// Given
	isolateStorage(t)

	// When / Then
	if _, err := Load(saasURL); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Load = %v, want ErrNotLoggedIn", err)
	}
}

func TestCredentialsKindAccessors(t *testing.T) {
	t.Run("stored credentials without a kind are oauth (back-compat)", func(t *testing.T) {
		// Given
		c := &Credentials{AccessToken: "access-1"}

		// When / Then
		if k := c.EffectiveKind(); k != KindOAuth {
			t.Errorf("EffectiveKind = %q, want %q", k, KindOAuth)
		}
		if tok := c.Token(); tok != "access-1" {
			t.Errorf("Token = %q, want the access token", tok)
		}
	})

	t.Run("master credentials expose the master key as the token", func(t *testing.T) {
		// Given
		c := &Credentials{Kind: KindMaster, MasterKey: "AbCd1234.secret"}

		// When / Then
		if tok := c.Token(); tok != "AbCd1234.secret" {
			t.Errorf("Token = %q, want the master key", tok)
		}
	})
}
