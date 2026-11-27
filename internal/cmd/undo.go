package cmd

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/v2/internal/api"
)

// undoFlagMutation returns the command that reverts m, given the state it
// changed (before is nil when m creates a segment override). It returns false
// when the previous state can't be restated as a command — an unset value, or
// re-weighted variants — so that no hint is better than a wrong one.
func undoFlagMutation(cmd *cobra.Command, name string, segmentID int, before *api.FeatureState, prevPriority int, m flagMutation) (string, bool) {
	scope := scopeArgs(cmd, segmentID)
	if segmentID != 0 && before == nil {
		return strings.Join(append([]string{"flagsmith", "flag", "delete", shellQuote(name)}, scope...), " "), true
	}
	if before == nil || len(m.weights) > 0 {
		return "", false
	}

	args := []string{"flagsmith", "flag", "update", shellQuote(name)}
	args = append(args, scope...)
	if (m.enable || m.disable) && before.Enabled != m.enable {
		if before.Enabled {
			args = append(args, "--enable")
		} else {
			args = append(args, "--disable")
		}
	}
	if m.setValue {
		value, ok := valueArgs(before.Value)
		if !ok {
			return "", false
		}
		args = append(args, value...)
	}
	if m.setPriority && prevPriority != m.priority {
		args = append(args, "--priority", strconv.Itoa(prevPriority))
	}
	if len(args) == 4+len(scope) {
		return "", false // nothing changed that needs reverting
	}
	return strings.Join(args, " "), true
}

// scopeArgs repeats the targeting the user gave explicitly, so the undo command
// hits the same project, environment and override from any directory.
func scopeArgs(cmd *cobra.Command, segmentID int) []string {
	var args []string
	for _, name := range []string{"project", "environment"} {
		if f := cmd.Flags().Lookup(name); f != nil && f.Changed {
			args = append(args, "--"+name, shellQuote(f.Value.String()))
		}
	}
	if segmentID != 0 {
		args = append(args, "--segment", strconv.Itoa(segmentID))
	}
	return args
}

// valueArgs restates a stored flag value as --value (and --type when inference
// would read it differently). A flag with no value can't be restored this way.
func valueArgs(v any) ([]string, bool) {
	switch v := v.(type) {
	case bool:
		return []string{"--value", strconv.FormatBool(v)}, true
	case float64:
		if v != math.Trunc(v) || math.Abs(v) > 1<<53 {
			return nil, false
		}
		return []string{"--value", strconv.FormatInt(int64(v), 10)}, true
	case int:
		return []string{"--value", strconv.Itoa(v)}, true
	case string:
		args := []string{"--value", shellQuote(v)}
		if inferred, _ := inferFeatureValue(v, ""); inferred.Type != "string" {
			args = append(args, "--type", "string")
		}
		return args, true
	default:
		return nil, false
	}
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./:@%+=,-]+$`)

// shellQuote returns s ready to paste into a POSIX shell.
func shellQuote(s string) string {
	if shellSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// printUndo writes the undo hint after a successful mutation.
func printUndo(cmd *cobra.Command, undo string, ok bool) {
	if ok {
		fmt.Fprintf(cmd.ErrOrStderr(), "  Undo: %s\n", undo)
	}
}
