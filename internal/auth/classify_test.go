package auth

import (
	"strings"
	"testing"
)

func TestClassifyAPIKey(t *testing.T) {
	t.Run("dotted value is a master API key", func(t *testing.T) {
		// Given
		value := "AbCd1234.0123456789abcdefABCDEF0123456789"

		// When
		kind, err := ClassifyAPIKey(value)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if kind != KindMaster {
			t.Errorf("kind = %q, want %q", kind, KindMaster)
		}
	})

	t.Run("dotless value is a bearer token", func(t *testing.T) {
		// Given
		value := "IpuuPoNaSCXPoXi2uc4QkgDCEytYDu"

		// When
		kind, err := ClassifyAPIKey(value)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if kind != KindBearer {
			t.Errorf("kind = %q, want %q", kind, KindBearer)
		}
	})

	t.Run("server-side environment key is rejected with pointer to the right variable", func(t *testing.T) {
		// Given
		value := "ser.AbCdEf1234"

		// When
		_, err := ClassifyAPIKey(value)

		// Then
		if err == nil || !strings.Contains(err.Error(), "FLAGSMITH_ENVIRONMENT_KEY") {
			t.Errorf("err = %v, want mention of FLAGSMITH_ENVIRONMENT_KEY", err)
		}
	})

	t.Run("legacy 40-hex authtoken is rejected as unsupported", func(t *testing.T) {
		// Given
		value := "0123456789abcdef0123456789abcdef01234567"

		// When
		_, err := ClassifyAPIKey(value)

		// Then
		if err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Errorf("err = %v, want an unsupported-authtoken error", err)
		}
	})

	t.Run("40 alphanumeric non-hex chars is still a bearer token", func(t *testing.T) {
		// Given
		value := strings.Repeat("Zz9x", 10)

		// When
		kind, err := ClassifyAPIKey(value)

		// Then
		if err != nil {
			t.Fatal(err)
		}
		if kind != KindBearer {
			t.Errorf("kind = %q, want %q", kind, KindBearer)
		}
	})
}
