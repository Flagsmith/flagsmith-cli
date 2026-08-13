package cmd

import (
	"os"
	"testing"
)

// The host suffix encodes host and port so a credential variable can name the
// instance it belongs to: `-` doubles, `.` and `:` become single underscores.
func TestScopedEnvName(t *testing.T) {
	cases := map[string]string{
		"https://api.flagsmith.com":          "FLAGSMITH_API_KEY_api_flagsmith_com",
		"https://api.flagsmith.com/":         "FLAGSMITH_API_KEY_api_flagsmith_com",
		"https://flagsmith.corp-internal.io": "FLAGSMITH_API_KEY_flagsmith_corp__internal_io",
		"http://localhost:8000":              "FLAGSMITH_API_KEY_localhost_8000",
		"http://127.0.0.1:8000/api":          "FLAGSMITH_API_KEY_127_0_0_1_8000",
		"https://Flagsmith.Example.COM":      "FLAGSMITH_API_KEY_flagsmith_example_com",
	}
	for in, want := range cases {
		if got := scopedEnvName(envAPIKey, in); got != want {
			t.Errorf("scopedEnvName(%q) = %q, want %q", in, got, want)
		}
	}
}

// The variable a user should set is the one that will actually be read: the
// unscoped form only where it is trusted, the host-scoped form everywhere else.
func TestEnvVarFor(t *testing.T) {
	cases := map[string]string{
		"https://api.flagsmith.com":     envAPIKey,
		"https://api.flagsmith.com/":    envAPIKey,
		"https://API.Flagsmith.com":     envAPIKey,
		"https://flagsmith.example.com": "FLAGSMITH_API_KEY_flagsmith_example_com",
		"http://localhost:8000":         "FLAGSMITH_API_KEY_localhost_8000",
	}
	for in, want := range cases {
		if got := envVarFor(envAPIKey, in, defaultAPIURL); got != want {
			t.Errorf("envVarFor(%q) = %q, want %q", in, got, want)
		}
	}
}

// Boolean switches read a value, not merely presence: FLAGSMITH_NO_INPUT=false
// must not disable prompting.
func TestEnvBool(t *testing.T) {
	cases := map[string]bool{
		"":      false,
		"0":     false,
		"false": false,
		"FALSE": false,
		"no":    false,
		"off":   false,
		" 0 ":   false,
		"1":     true,
		"true":  true,
		"yes":   true,
		"on":    true,
	}
	for in, want := range cases {
		t.Setenv("FLAGSMITH_TEST_BOOL", in)
		if got := envBool("FLAGSMITH_TEST_BOOL"); got != want {
			t.Errorf("envBool(%q) = %v, want %v", in, got, want)
		}
	}
	os.Unsetenv("FLAGSMITH_TEST_BOOL")
	if envBool("FLAGSMITH_TEST_BOOL") {
		t.Error("envBool on an unset variable = true, want false")
	}
}

// An unscoped credential is trusted only for the surface's default host; a
// host-scoped one is trusted for its own host whatever named it, and ignored
// elsewhere. A miss reports no credential so resolution falls through.
func TestEnvCredential(t *testing.T) {
	const key = "AbCd1234.0123456789abcdefABCDEF01234567"
	const self = "https://flagsmith.example"

	t.Run("unscoped serves the default host", func(t *testing.T) {
		// Given
		t.Setenv(envAPIKey, key)

		// When / Then
		name, got := envCredential(envAPIKey, defaultAPIURL, defaultAPIURL)
		if name != envAPIKey || got != key {
			t.Errorf("= (%q, %q), want the unscoped variable", name, got)
		}
	})

	t.Run("unscoped is withheld from a non-default host", func(t *testing.T) {
		// Given
		t.Setenv(envAPIKey, key)

		// When / Then
		if name, got := envCredential(envAPIKey, self, defaultAPIURL); name != "" || got != "" {
			t.Errorf("= (%q, %q), want no credential for a redirected host", name, got)
		}
	})

	t.Run("host-scoped serves its own host", func(t *testing.T) {
		// Given
		t.Setenv("FLAGSMITH_API_KEY_flagsmith_example", key)

		// When / Then
		name, got := envCredential(envAPIKey, self, defaultAPIURL)
		if name != "FLAGSMITH_API_KEY_flagsmith_example" || got != key {
			t.Errorf("= (%q, %q), want the scoped variable", name, got)
		}
	})

	t.Run("host-scoped is ignored for another host", func(t *testing.T) {
		// Given
		t.Setenv("FLAGSMITH_API_KEY_flagsmith_example", key)

		// When / Then
		if name, _ := envCredential(envAPIKey, "https://other.example", defaultAPIURL); name != "" {
			t.Errorf("name = %q, want the scope not to apply elsewhere", name)
		}
	})

	t.Run("scoped outranks unscoped on the default host", func(t *testing.T) {
		// Given
		t.Setenv(envAPIKey, "unscoped-value")
		t.Setenv("FLAGSMITH_API_KEY_api_flagsmith_com", key)

		// When / Then
		name, got := envCredential(envAPIKey, defaultAPIURL, defaultAPIURL)
		if name != "FLAGSMITH_API_KEY_api_flagsmith_com" || got != key {
			t.Errorf("= (%q, %q), want the more specific variable", name, got)
		}
	})

	t.Run("the variable name matches case-insensitively", func(t *testing.T) {
		// Given
		t.Setenv("FLAGSMITH_API_KEY_FLAGSMITH_EXAMPLE", key)

		// When / Then
		if _, got := envCredential(envAPIKey, self, defaultAPIURL); got != key {
			t.Errorf("value = %q, want an uppercase scoped variable to match", got)
		}
	})

	t.Run("scheme is not part of the scope", func(t *testing.T) {
		// Given
		t.Setenv("FLAGSMITH_API_KEY_flagsmith_example", key)

		// When / Then
		if _, got := envCredential(envAPIKey, "http://flagsmith.example", defaultAPIURL); got != key {
			t.Errorf("value = %q, want one scope to cover either scheme", got)
		}
	})
}
