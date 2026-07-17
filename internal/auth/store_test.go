package auth

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/zalando/go-keyring"
)

const (
	saasURL = "https://api.flagsmith.com"
	selfURL = "https://flagsmith.acme.internal"
)

// isolateStorage points both storage backends at test-owned locations:
// the keychain at go-keyring's in-memory mock, and the fallback file at a
// temp directory (via the env vars os.UserConfigDir derives from).
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
	source, err := Save(want)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if source != SourceKeychain {
		t.Errorf("Save source = %q, want %q", source, SourceKeychain)
	}

	// When
	got, source, err := Load(saasURL)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if source != SourceKeychain {
		t.Errorf("Load source = %q, want %q", source, SourceKeychain)
	}
	assertCredentialsEqual(t, got, want)

	// When
	if err := Delete(saasURL); err != nil {
		t.Fatal(err)
	}

	// Then
	if _, _, err := Load(saasURL); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Load after Delete = %v, want ErrNotLoggedIn", err)
	}
}

func TestStoreKeysByInstance(t *testing.T) {
	// Given
	isolateStorage(t)
	saas := oauthCredentials(saasURL)
	self := &Credentials{Kind: KindMaster, APIURL: selfURL, MasterKey: "AbCd1234.secret"}
	if _, err := Save(saas); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(self); err != nil {
		t.Fatal(err)
	}

	// When / Then
	gotSaas, _, err := Load(saasURL)
	if err != nil {
		t.Fatal(err)
	}
	assertCredentialsEqual(t, gotSaas, saas)
	gotSelf, _, err := Load(selfURL)
	if err != nil {
		t.Fatal(err)
	}
	assertCredentialsEqual(t, gotSelf, self)

	// When
	if err := Delete(selfURL); err != nil {
		t.Fatal(err)
	}

	// Then
	if _, _, err := Load(selfURL); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Load(self) after Delete(self) = %v, want ErrNotLoggedIn", err)
	}
	if _, _, err := Load(saasURL); err != nil {
		t.Errorf("Load(saas) after Delete(self) = %v, want untouched", err)
	}
}

func TestStoreNormalizesInstanceURL(t *testing.T) {
	// Given
	isolateStorage(t)
	want := oauthCredentials(saasURL)
	if _, err := Save(want); err != nil {
		t.Fatal(err)
	}

	// When
	got, _, err := Load(saasURL + "/")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	assertCredentialsEqual(t, got, want)
}

func TestStoreFileFallback(t *testing.T) {
	// Given
	isolateStorage(t)
	keyring.MockInitWithError(errors.New("keychain locked"))
	want := oauthCredentials(saasURL)
	other := oauthCredentials(selfURL)

	// When
	source, err := Save(want)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if source != SourceFile {
		t.Errorf("Save source = %q, want %q", source, SourceFile)
	}
	if _, err := Save(other); err != nil {
		t.Fatal(err)
	}
	path, err := CredentialsFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("credentials file mode = %o, want 0600", fi.Mode().Perm())
		}
	}

	// When
	got, source, err := Load(saasURL)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if source != SourceFile {
		t.Errorf("Load source = %q, want %q", source, SourceFile)
	}
	assertCredentialsEqual(t, got, want)

	// When
	if err := Delete(saasURL); err != nil {
		t.Fatal(err)
	}

	// Then
	if _, _, err := Load(saasURL); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Load after Delete = %v, want ErrNotLoggedIn", err)
	}
	if _, _, err := Load(selfURL); err != nil {
		t.Errorf("Load(other) after Delete(saas) = %v, want untouched", err)
	}
}

func TestLoadNotLoggedIn(t *testing.T) {
	// Given
	isolateStorage(t)

	// When / Then
	if _, _, err := Load(saasURL); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Load = %v, want ErrNotLoggedIn", err)
	}
}

func TestLoadCorruptFile(t *testing.T) {
	// Given
	isolateStorage(t)
	path, err := CredentialsFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	// When / Then
	if _, _, err := Load(saasURL); err == nil || errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Load with corrupt file = %v, want a corruption error", err)
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
