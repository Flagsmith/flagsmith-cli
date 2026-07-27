package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAbbreviateHome(t *testing.T) {
	sep := string(os.PathSeparator)
	home := filepath.Join(sep+"Users", "kim")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cases := map[string]string{
		filepath.Join(home, "flagsmith-demo", "flagsmith.json"): "~" + sep + filepath.Join("flagsmith-demo", "flagsmith.json"),
		home: "~",
		filepath.Join(sep+"etc", "flagsmith.json"): filepath.Join(sep+"etc", "flagsmith.json"),
		home + "ble" + sep + "x":                   home + "ble" + sep + "x", // must respect the path boundary
	}
	for in, want := range cases {
		if got := abbreviateHome(in); got != want {
			t.Errorf("abbreviateHome(%q) = %q, want %q", in, got, want)
		}
	}
}
