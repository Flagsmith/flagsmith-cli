package auth

import (
	"errors"
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

	t.Run("dotless value is rejected as not a master key", func(t *testing.T) {
		// Given
		value := "IpuuPoNaSCXPoXi2uc4QkgDCEytYDu"

		// When
		err := ValidateMasterKey(value)

		// Then
		if !errors.Is(err, ErrNotMasterKey) {
			t.Errorf("err = %v, want ErrNotMasterKey", err)
		}
	})

	t.Run("server-side environment key is rejected", func(t *testing.T) {
		// Given
		value := "ser.AbCdEf1234"

		// When
		err := ValidateMasterKey(value)

		// Then
		if !errors.Is(err, ErrServerSideKey) {
			t.Errorf("err = %v, want ErrServerSideKey", err)
		}
	})

	t.Run("legacy 40-hex authtoken is rejected as unsupported", func(t *testing.T) {
		// Given
		value := "0123456789abcdef0123456789abcdef01234567"

		// When
		err := ValidateMasterKey(value)

		// Then
		if !errors.Is(err, ErrLegacyAuthtoken) {
			t.Errorf("err = %v, want ErrLegacyAuthtoken", err)
		}
	})
}
