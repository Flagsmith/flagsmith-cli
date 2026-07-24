package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/internal/bug"
	"github.com/Flagsmith/flagsmith-cli/internal/config"
)

func TestHintFor(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"not logged in", auth.ErrNotLoggedIn, hintLogin},
		{"plan gated", api.ErrPlanGated, hintPricing},
		{"plan gated wrapped", fmt.Errorf("create project: %w", api.ErrPlanGated), hintPricing},
		{"workflow gated", api.ErrWorkflowGated, docsHint("advanced-use/change-requests")},
		{"keychain unavailable", auth.ErrKeychainUnavailable, hintMasterKey},
		{"session refresh failed wrapped", fmt.Errorf("%w: %w", auth.ErrRefreshFailed, errors.New("boom")), hintRelogin},
		{"server-side key in FLAGSMITH_API_KEY", auth.ErrServerSideKey, hintServerSideKey},
		{"legacy authtoken", auth.ErrLegacyAuthtoken, hintMasterKeyOrLogin},
		{"not a master key", auth.ErrNotMasterKey, hintAccessToken},
		{"server-side key in config file", fmt.Errorf("flagsmith.json: %w", config.ErrServerSideKey), hintServerSideKey},
		{"marked unexpected", bug.Mark(errors.New("boom")), hintReportIssue},
		{"specific hint beats report-issue", bug.Mark(fmt.Errorf("%w: %w", auth.ErrRefreshFailed, errors.New("boom"))), hintRelogin},
		{"explicit hint wins over automatic", withHint(api.ErrPlanGated, "custom"), "custom"},
		{"explicit hint on plain error", hintf(errors.New("boom"), "run %s", "flagsmith login"), "run flagsmith login"},
		{"no hint", errors.New("plain"), ""},
		{"nil", nil, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hintFor(tt.err); got != tt.want {
				t.Errorf("hintFor = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReportError(t *testing.T) {
	newCmd := func(buf *bytes.Buffer) *cobra.Command {
		c := &cobra.Command{Use: "demo", Short: "demo"}
		c.SetErr(buf)
		return c
	}

	t.Run("usage error prints usage and exits 2", func(t *testing.T) {
		var buf bytes.Buffer
		code := reportError(newCmd(&buf), usageErrorf("bad input"))
		if code != 2 {
			t.Errorf("code = %d, want 2", code)
		}
		if !strings.Contains(buf.String(), "Usage:") {
			t.Errorf("no usage block: %q", buf.String())
		}
	})

	t.Run("runtime error prints no usage and exits 1", func(t *testing.T) {
		var buf bytes.Buffer
		code := reportError(newCmd(&buf), errors.New("network down"))
		if code != 1 {
			t.Errorf("code = %d, want 1", code)
		}
		if strings.Contains(buf.String(), "Usage:") {
			t.Errorf("unexpected usage block: %q", buf.String())
		}
	})

	t.Run("hint precedes usage", func(t *testing.T) {
		var buf bytes.Buffer
		reportError(newCmd(&buf), hintf(usageErrorf("bad"), "try flagsmith foo"))
		out := buf.String()
		i, j := strings.Index(out, "try flagsmith foo"), strings.Index(out, "Usage:")
		if i < 0 || j < 0 || i > j {
			t.Errorf("hint should precede usage: %q", out)
		}
	})

	t.Run("plan-gated hint surfaces automatically", func(t *testing.T) {
		var buf bytes.Buffer
		reportError(newCmd(&buf), api.ErrPlanGated)
		if !strings.Contains(buf.String(), "pricing") {
			t.Errorf("no pricing hint: %q", buf.String())
		}
	})
}

// Every runnable command carries an examples block (02 §4). Container commands
// (no RunE) and cobra's built-ins are exempt.
func TestEveryCommandHasExamples(t *testing.T) {
	var missing []string
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			switch sub.Name() {
			case "help", "completion":
				continue
			}
			if sub.Runnable() && !sub.Hidden && sub.Example == "" {
				missing = append(missing, sub.CommandPath())
			}
			walk(sub)
		}
	}
	walk(rootCmd)
	if len(missing) > 0 {
		t.Errorf("commands without an examples block:\n\t%s", strings.Join(missing, "\n\t"))
	}
}

// Incorrect usage (bad arg count, unknown flag) exits 2 and prints usage, even
// for cobra's own parse/validation failures — see 02 §4.
func TestUsageErrorsPrintUsage(t *testing.T) {
	assertUsage := func(t *testing.T, out string, err error) {
		t.Helper()
		var ue *usageError
		if !errors.As(err, &ue) {
			t.Fatalf("err = %v, want usageError", err)
		}
		if !strings.Contains(out, "Usage:") {
			t.Errorf("no usage in output: %q", out)
		}
	}

	t.Run("wrong argument count", func(t *testing.T) {
		out, err := run("", "environment", "get") // requires exactly 1 arg
		assertUsage(t, out, err)
	})

	t.Run("unknown flag", func(t *testing.T) {
		out, err := run("", "flag", "list", "--bogus")
		assertUsage(t, out, err)
	})
}
