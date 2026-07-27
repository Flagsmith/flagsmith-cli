package cmd

import "testing"

func TestAbbreviateHome(t *testing.T) {
	t.Setenv("HOME", "/Users/kim")
	cases := map[string]string{
		"/Users/kim/flagsmith-demo/flagsmith.json": "~/flagsmith-demo/flagsmith.json",
		"/Users/kim":          "~",
		"/etc/flagsmith.json": "/etc/flagsmith.json",
		"/Users/kimble/x":     "/Users/kimble/x", // must respect the path boundary
	}
	for in, want := range cases {
		if got := abbreviateHome(in); got != want {
			t.Errorf("abbreviateHome(%q) = %q, want %q", in, got, want)
		}
	}
}
