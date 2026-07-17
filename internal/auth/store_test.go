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

func testCredentials() *Credentials {
	return &Credentials{
		APIURL:       "http://127.0.0.1:8000",
		AccessToken:  "access-1",
		RefreshToken: "refresh-1",
		ExpiresAt:    time.Now().Add(15 * time.Minute).Truncate(time.Second),
	}
}

func assertCredentialsEqual(t *testing.T, got, want *Credentials) {
	t.Helper()
	if got.APIURL != want.APIURL ||
		got.AccessToken != want.AccessToken ||
		got.RefreshToken != want.RefreshToken ||
		!got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("credentials round-trip mismatch:\ngot  %+v\nwant %+v", got, want)
	}
}

func TestStoreKeychainRoundTrip(t *testing.T) {
	isolateStorage(t)
	want := testCredentials()

	source, err := Save(want)
	if err != nil {
		t.Fatal(err)
	}
	if source != SourceKeychain {
		t.Errorf("Save source = %q, want %q", source, SourceKeychain)
	}

	got, source, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if source != SourceKeychain {
		t.Errorf("Load source = %q, want %q", source, SourceKeychain)
	}
	assertCredentialsEqual(t, got, want)

	if err := Delete(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Load after Delete = %v, want ErrNotLoggedIn", err)
	}
}

func TestStoreFileFallback(t *testing.T) {
	isolateStorage(t)
	keyring.MockInitWithError(errors.New("keychain locked"))
	want := testCredentials()

	source, err := Save(want)
	if err != nil {
		t.Fatal(err)
	}
	if source != SourceFile {
		t.Errorf("Save source = %q, want %q", source, SourceFile)
	}

	path, err := credentialsPath()
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

	got, source, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if source != SourceFile {
		t.Errorf("Load source = %q, want %q", source, SourceFile)
	}
	assertCredentialsEqual(t, got, want)

	// Delete must succeed even with the keychain broken (best-effort).
	if err := Delete(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Load after Delete = %v, want ErrNotLoggedIn", err)
	}
}

func TestLoadNotLoggedIn(t *testing.T) {
	isolateStorage(t)
	if _, _, err := Load(); !errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Load = %v, want ErrNotLoggedIn", err)
	}
}

func TestLoadCorruptFile(t *testing.T) {
	isolateStorage(t)
	path, err := credentialsPath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(); err == nil || errors.Is(err, ErrNotLoggedIn) {
		t.Errorf("Load with corrupt file = %v, want a corruption error", err)
	}
}
