package version

import "testing"

func TestIsRelease(t *testing.T) {
	cases := map[string]bool{
		"v1.2.3":                             true,
		"v0.1.0":                             true,
		"v1.2.3-rc1":                         false,
		"v0.0.0-20260101120000-abcdef123456": false, // go install pseudo-version
		"dev":                                false,
		"dev (2f71d6f)":                      false,
		"feat/cli-v2":                        false,
		"1.2.3":                              false,
		"":                                   false,
	}
	for in, want := range cases {
		if got := IsRelease(in); got != want {
			t.Errorf("IsRelease(%q) = %v, want %v", in, got, want)
		}
	}
}
