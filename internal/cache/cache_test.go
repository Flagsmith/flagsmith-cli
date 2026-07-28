package cache

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func isolate(t *testing.T) {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, ".cache"))
	t.Setenv("LocalAppData", tmp)
}

func TestNamesRoundTrip(t *testing.T) {
	// Given
	isolate(t)
	const instance = "https://api.flagsmith.com"

	// When
	err := Merge(instance, &Names{
		Organisations: map[string]string{"3": "Acme"},
		Projects:      map[string]string{"12345": "my-app"},
	})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	got := Load(instance)
	if got.Organisations["3"] != "Acme" || got.Projects["12345"] != "my-app" {
		t.Errorf("Load = %+v", got)
	}
}

func TestMergeAccumulates(t *testing.T) {
	// Given
	isolate(t)
	const instance = "https://api.flagsmith.com"
	if err := Merge(instance, &Names{Projects: map[string]string{"1": "one"}}); err != nil {
		t.Fatal(err)
	}

	// When
	if err := Merge(instance, &Names{
		Projects:     map[string]string{"2": "two"},
		Environments: map[string]string{"WqXhZk8s": "Development"},
	}); err != nil {
		t.Fatal(err)
	}

	// Then
	got := Load(instance)
	if got.Projects["1"] != "one" || got.Projects["2"] != "two" ||
		got.Environments["WqXhZk8s"] != "Development" {
		t.Errorf("Load = %+v", got)
	}
}

func TestInstancesAreIsolated(t *testing.T) {
	// Given
	isolate(t)
	if err := Merge("https://a.example", &Names{Projects: map[string]string{"1": "a"}}); err != nil {
		t.Fatal(err)
	}

	// When
	got := Load("https://b.example")

	// Then
	if len(got.Projects) != 0 {
		t.Errorf("Load(b) = %+v, want empty", got)
	}
}

func TestLoadMissIsEmptyNotNil(t *testing.T) {
	// Given
	isolate(t)

	// When
	got := Load("https://api.flagsmith.com")

	// Then
	if got == nil {
		t.Fatal("Load = nil, want an empty cache")
	}
	if got.Projects["1"] != "" {
		t.Error("expected empty lookups to return zero values")
	}
}

// The cache holds organisation, project and segment names for one instance —
// not secrets, but nothing else on this machine needs them either, and the
// CLI writes nothing else to disk.
func TestMergeWritesPrivately(t *testing.T) {
	if runtime.GOOS == "windows" {
		// Windows has no POSIX mode bits: os.Stat synthesises 0666/0777 and
		// only the read-only flag round-trips, so the modes we pass are
		// unobservable. Access there comes from the ACL that %LocalAppData%
		// (os.UserCacheDir) already restricts to the user.
		t.Skip("file modes are not POSIX on Windows")
	}
	// Given
	isolate(t)

	// When
	if err := Merge("https://api.flagsmith.com", &Names{Projects: map[string]string{"1": "one"}}); err != nil {
		t.Fatal(err)
	}

	// Then the file is owner-only, and so is the directory holding it
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache file mode = %04o, want 0600", perm)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("cache dir mode = %04o, want 0700", perm)
	}
}
