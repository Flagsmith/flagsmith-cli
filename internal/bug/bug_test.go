package bug

import (
	"errors"
	"fmt"
	"testing"
)

func TestMark(t *testing.T) {
	t.Run("nil stays nil", func(t *testing.T) {
		if Mark(nil) != nil {
			t.Error("Mark(nil) != nil")
		}
	})

	t.Run("marked error is recognisable", func(t *testing.T) {
		if !errors.Is(Mark(errors.New("boom")), ErrUnexpected) {
			t.Error("Mark(err) is not ErrUnexpected")
		}
	})

	t.Run("message is unchanged", func(t *testing.T) {
		if got := Mark(errors.New("boom")).Error(); got != "boom" {
			t.Errorf("Error() = %q, want %q", got, "boom")
		}
	})

	t.Run("wrapped causes stay visible through the mark", func(t *testing.T) {
		cause := errors.New("cause")
		if !errors.Is(Mark(fmt.Errorf("ctx: %w", cause)), cause) {
			t.Error("cause not visible through Mark")
		}
	})

	t.Run("unmarked error is not ErrUnexpected", func(t *testing.T) {
		if errors.Is(errors.New("boom"), ErrUnexpected) {
			t.Error("plain error reads as ErrUnexpected")
		}
	})
}
