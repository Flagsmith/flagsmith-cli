package cmd

import "testing"

// label is the one home for the "Name (identifier)" display form. It assumes a
// real name or none; splitting an id-or-name reference is nameRef's job.
func TestLabel(t *testing.T) {
	cases := []struct {
		name string
		id   any
		want string
	}{
		{"acme-api", 101, "acme-api (101)"},
		{"", 101, "101"},
		{"Production", "K2mVsGdXhZ8k", "Production (K2mVsGdXhZ8k)"},
		{"", "K2mVsGdXhZ8k", "K2mVsGdXhZ8k"},
	}
	for _, c := range cases {
		if got := label(c.name, c.id); got != c.want {
			t.Errorf("label(%q, %v) = %q, want %q", c.name, c.id, got, c.want)
		}
	}
}

// nameRef splits an id-or-name reference for display: only a real name may
// precede the "(id)".
func TestNameRef(t *testing.T) {
	cases := map[string]string{
		"acme-api": "acme-api",
		"44626":    "",
		"":         "",
	}
	for in, want := range cases {
		if got := nameRef(in); got != want {
			t.Errorf("nameRef(%q) = %q, want %q", in, got, want)
		}
	}
}
