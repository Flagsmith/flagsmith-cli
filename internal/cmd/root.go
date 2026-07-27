package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Flagsmith/flagsmith-cli/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

// annotationLongRunning marks a command that manages its own long wait (e.g. a
// browser login) and so opts out of the overall per-invocation deadline.
const annotationLongRunning = "longRunning"

// defaultTimeout is the overall deadline applied to a command's context, so a
// stuck network call fails instead of hanging indefinitely.
const defaultTimeout = 60 * time.Second

// commandTimeout is the per-invocation deadline. FLAGSMITH_TIMEOUT overrides it
// with a whole number of seconds; an explicit 0 disables the cap (context
// cancellation still works). A malformed value falls back to the default.
func commandTimeout() time.Duration {
	if v := os.Getenv("FLAGSMITH_TIMEOUT"); v != "" {
		secs, err := strconv.Atoi(v)
		if err != nil {
			return defaultTimeout
		}
		if secs <= 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	return defaultTimeout
}

// cancelTimeout releases the deadline installed by PersistentPreRunE; Execute
// calls it once the command has run.
var cancelTimeout context.CancelFunc

// singleLineUsage rewrites cobra's default two-line Usage block (one line for
// running the command bare, another for a subcommand) into a single
// context-appropriate line: "<path> [command] [flags]" for groups, or the
// command's own use line for leaves. Applied as a string replace on cobra's
// own template so it survives version bumps (a no-op if the block ever changes,
// caught by TestUsageIsSingleLine).
func singleLineUsage(template string) string {
	const defaultBlock = "Usage:{{if .Runnable}}\n  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}\n  {{.CommandPath}} [command]{{end}}"
	const oneLine = "Usage:{{if .HasAvailableSubCommands}}\n  {{.CommandPath}} [command] [flags]{{else}}\n  {{.UseLine}}{{end}}"
	return strings.Replace(template, defaultBlock, oneLine, 1)
}

// apiURL is the resolved instance URL for the current invocation, set by
// applyContext. Flag values live in the *Flag variables below.
var apiURL string

var (
	apiURLFlag       string
	sdkAPIURLFlag    string
	projectFlag      string
	organisationFlag string
	environmentFlag  string
	configPathFlag   string
	yesFlag          bool
	noInputFlag      bool
	jsonFlag         bool
	jqFlag           string
)

// jsonOutput reports whether machine-readable output was requested.
func jsonOutput() bool {
	return jsonFlag || jqFlag != "" || os.Getenv("FLAGSMITH_JSON_OUTPUT") != ""
}

// outputOpts is the render format for the current invocation.
func outputOpts() output.Options {
	return output.Options{JSON: jsonOutput(), JQ: jqFlag}
}

// usageError is a missing/invalid input a prompt would have collected
// interactively; it exits with code 2 (see 02-output-and-interactivity).
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usageErrorf(format string, a ...any) error {
	return &usageError{msg: fmt.Sprintf(format, a...)}
}

var rootCmd = &cobra.Command{
	Use:          "flagsmith",
	Short:        "The Flagsmith command-line interface",
	SilenceUsage: true,
	// Drop any memoised credential from a prior run before each invocation
	// (matters in-process, e.g. tests reusing rootCmd across Execute calls).
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		resetCredentialCache()
		// Give the command a sane overall deadline so a stuck network call
		// fails instead of hanging. Long-running commands (browser login) opt
		// out; cancellation via the parent context still propagates.
		if cmd.Annotations[annotationLongRunning] != "true" {
			if d := commandTimeout(); d > 0 {
				ctx, cancel := context.WithTimeout(cmd.Context(), d)
				cmd.SetContext(ctx)
				cancelTimeout = cancel
			}
		}
		return nil
	},
	RunE: func(cmd *cobra.Command, args []string) error {
		out := cmd.OutOrStdout()
		fmt.Fprintf(out, "%s.\n\n", cmd.Short)
		if nudgeInit(cmd) {
			fmt.Fprint(out, "Don't know where to start? Try:\n  flagsmith init\n\n")
		}
		fmt.Fprint(out, cmd.UsageString())
		return nil
	},
}

// nudgeInit reports whether the user has neither project context nor
// credentials — the state flagsmith init exists to fix.
func nudgeInit(cmd *cobra.Command) bool {
	pc, err := resolveContext(cmd)
	if err != nil || pc.ConfigPath.Value != nil {
		return false
	}
	if os.Getenv(envAPIKey) != "" || os.Getenv(envAccessToken) != "" {
		return false
	}
	if _, err := auth.Load(pc.apiURL()); !errors.Is(err, auth.ErrNotLoggedIn) {
		return false
	}
	return true
}

func Execute() {
	prepare()
	// ExecuteC returns the command that actually ran (or failed to parse), so a
	// usageError can print the nearest command's usage — not the root's.
	cmd, err := rootCmd.ExecuteC()
	if cancelTimeout != nil {
		cancelTimeout()
		cancelTimeout = nil
	}
	if err == nil {
		return
	}
	os.Exit(reportError(cmd, err))
}

// reportError renders an error's hint and, for incorrect-input (exit 2) errors,
// the nearest command's usage. cobra has already printed "Error: <message>";
// this appends the hint then the usage block, and returns the process exit
// code. Split from Execute so tests can exercise the rendering without os.Exit.
func reportError(cmd *cobra.Command, err error) int {
	errOut := cmd.ErrOrStderr()
	if hint := hintFor(err); hint != "" {
		fmt.Fprintln(errOut, hint)
	}
	var usage *usageError
	if errors.As(err, &usage) {
		// Hidden commands aren't advertised, so don't print their usage — e.g.
		// the `flag create` redirect, which has no real usage to show. Still
		// exit 2: it's incorrect input.
		if !cmd.Hidden {
			fmt.Fprint(errOut, cmd.UsageString())
		}
		return 2
	}
	return 1
}

// prepare wires up one-time behaviour that must see every registered command:
// incorrect positional-argument counts become usageErrors, so cobra's own
// arg-validation failures print usage and exit 2 like our other usage errors.
// Idempotent — safe to call before each invocation (Execute and tests).
var prepareOnce sync.Once

func prepare() {
	prepareOnce.Do(func() { usageArgs(rootCmd) })
}

func usageArgs(cmd *cobra.Command) {
	if inner := cmd.Args; inner != nil {
		cmd.Args = func(c *cobra.Command, args []string) error {
			if err := inner(c, args); err != nil {
				return &usageError{msg: err.Error()}
			}
			return nil
		}
	}
	for _, sub := range cmd.Commands() {
		usageArgs(sub)
	}
}

func init() {
	flags := rootCmd.PersistentFlags()
	flags.StringVar(&apiURLFlag, "api-url", defaultAPIURL,
		"Flagsmith API base URL (env: FLAGSMITH_API_URL)")
	flags.StringVar(&sdkAPIURLFlag, "sdk-api-url", "",
		"SDK API base URL for flag evaluation (env: FLAGSMITH_SDK_API_URL)")
	flags.StringVarP(&projectFlag, "project", "p", "",
		"project ID or name (env: FLAGSMITH_PROJECT)")
	flags.StringVar(&organisationFlag, "organisation", "",
		"organisation ID or name (env: FLAGSMITH_ORGANISATION)")
	flags.StringVarP(&environmentFlag, "environment", "e", "",
		"environment as its client-side SDK key (env: FLAGSMITH_ENVIRONMENT)")
	flags.StringVarP(&configPathFlag, "config-path", "c", "",
		"path to flagsmith.json (env: FLAGSMITH_CONFIG_PATH)")
	flags.BoolVar(&yesFlag, "yes", false,
		"answer confirmations affirmatively")
	flags.BoolVar(&noInputFlag, "no-input", false,
		"never prompt or open a browser; fail if required input is missing (env: FLAGSMITH_NO_INPUT)")
	flags.BoolVar(&jsonFlag, "json", false,
		"output JSON instead of human-readable text (env: FLAGSMITH_JSON_OUTPUT)")
	flags.StringVar(&jqFlag, "jq", "",
		"filter JSON output through a jq expression (implies --json)")

	rootCmd.SetUsageTemplate(singleLineUsage(rootCmd.UsageTemplate()))

	// Flag-parse failures (unknown flag, missing value, bad value) are incorrect
	// usage: exit 2 and print the command's usage. Set on the root; cobra walks
	// up to it for every subcommand.
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return &usageError{msg: err.Error()}
	})

	// Hidden alias: --api for --api-url.
	rootCmd.SetGlobalNormalizationFunc(func(f *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "api" {
			name = "api-url"
		}
		return pflag.NormalizedName(name)
	})
}
