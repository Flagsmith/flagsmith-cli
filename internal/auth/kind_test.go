package auth

import (
	"strings"
	"testing"
)

func TestValidateMasterKey(t *testing.T) {
	t.Run("dotted value is a valid master API key", func(t *testing.T) {
		// Given
		value := "AbCd1234.0123456789abcdefABCDEF0123456789"

		// When
		err := ValidateMasterKey(value)

		// Then
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("dotless value is rejected and points at FLAGSMITH_ACCESS_TOKEN", func(t *testing.T) {
		// Given
		value := "IpuuPoNaSCXPoXi2uc4QkgDCEytYDu"

		// When
		err := ValidateMasterKey(value)

		// Then
		if err == nil || !strings.Contains(err.Error(), "FLAGSMITH_ACCESS_TOKEN") {
			t.Errorf("err = %v, want it to point at FLAGSMITH_ACCESS_TOKEN", err)
		}
	})

	t.Run("server-side environment key is rejected with pointer to the right variable", func(t *testing.T) {
		// Given
		value := "ser.AbCdEf1234"

		// When
		err := ValidateMasterKey(value)

		// Then
		if err == nil || !strings.Contains(err.Error(), "FLAGSMITH_ENVIRONMENT_KEY") {
			t.Errorf("err = %v, want mention of FLAGSMITH_ENVIRONMENT_KEY", err)
		}
	})

	t.Run("legacy 40-hex authtoken is rejected as unsupported", func(t *testing.T) {
		// Given
		value := "0123456789abcdef0123456789abcdef01234567"

		// When
		err := ValidateMasterKey(value)

		// Then
		if err == nil || !strings.Contains(err.Error(), "not supported") {
			t.Errorf("err = %v, want an unsupported-authtoken error", err)
		}
	})
}
