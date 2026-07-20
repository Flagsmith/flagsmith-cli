package cache

import (
	"path/filepath"
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
