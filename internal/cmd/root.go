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

	"github.com/Flagsmith/flagsmith-cli/v2/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/output"
	"github.com/Flagsmith/flagsmith-cli/v2/internal/version"
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

// cancelTimeout releases the deadline installed by PersistentPreRunE.
// timeoutParent is the context that deadline derives from, kept so it can be
// reinstalled fresh after interactive input.
var (
	cancelTimeout context.CancelFunc
	timeoutParent context.Context
)

// refreshCommandTimeout restarts the invocation deadline after interactive
// input: the deadline exists to catch stuck network calls, so time a human
// spends thinking at a prompt must not count against the requests that follow.
// A command without a deadline (long-running, or FLAGSMITH_TIMEOUT=0) is a no-op.
func refreshCommandTimeout(cmd *cobra.Command) {
	if cancelTimeout == nil || timeoutParent == nil {
		return
	}
	cancelTimeout()
	ctx, cancel := context.WithTimeout(timeoutParent, commandTimeout())
	cmd.SetContext(ctx)
	cancelTimeout = cancel
}

// singleLineUsage rewrites cobra's default two-line Usage block (one line for
// running the command bare, another for a subcommand) into a single
// context-appropriate line: "<path> [command] [flags]" for groups, or the
// command's own use line for leaves. Applied as a string replace on cobra's own
// template so it survives version bumps; a no-op if the block ever changes.
func singleLineUsage(template string) string {
	const defaultBlock = "Usage:{{if .Runnable}}\n  {{.UseLine}}{{end}}{{if .HasAvailableSubCommands}}\n  {{.CommandPath}} [command]{{end}}"
	const oneLine = "Usage:{{if .HasAvailableSubCommands}}\n  {{.CommandPath}} [command] [flags]{{else}}\n  {{.UseLine}}{{end}}"
	return strings.Replace(template, defaultBlock, oneLine, 1)
}

// apiURL and sdkAPIURL are the resolved Admin and SDK surface URLs for the
// current invocation, set by resolveContext as soon as each is known.
var (
	apiURL    string
	sdkAPIURL string
)

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
	return jsonFlag || jqFlag != "" || envBool("FLAGSMITH_JSON_OUTPUT")
}

// outputOpts is the render format for the current invocation.
func outputOpts() output.Options {
	return output.Options{JSON: jsonOutput(), JQ: jqFlag}
}

// usageError is a missing/invalid input a prompt would have collected
// interactively; it exits with code 2.
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
	// (matters in-process, where rootCmd is reused across Execute calls).
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		resetCredentialCache()
		initPrompts(cmd)
		// Give the command a sane overall deadline so a stuck network call
		// fails instead of hanging. Long-running commands (browser login) opt
		// out; cancellation via the parent context still propagates. The parent
		// is the root's context: cobra inherits it only while the leaf's is nil,
		// so deriving from the leaf would chain onto a previous in-process
		// invocation's (possibly expired) deadline. It is retained so time spent
		// at a prompt can be handed back.
		cancelTimeout, timeoutParent = nil, nil
		cmd.SetContext(cmd.Root().Context())
		if cmd.Annotations[annotationLongRunning] != "true" {
			if d := commandTimeout(); d > 0 {
				timeoutParent = cmd.Context()
				ctx, cancel := context.WithTimeout(timeoutParent, d)
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
	if _, v := envCredential(envAPIKey, pc.apiURL(), defaultAPIURL); v != "" {
		return false
	}
	if _, v := envCredential(envAccessToken, pc.apiURL(), defaultAPIURL); v != "" {
		return false
	}
	if _, err := auth.Load(pc.apiURL()); !errors.Is(err, auth.ErrNotLoggedIn) {
		return false
	}
	return true
}

// Root returns the fully wired command tree, for callers that need to inspect
// it rather than run it — documentation generation, shell completions. It never
// executes a command, so no credential or network access is involved.
func Root() *cobra.Command {
	prepare()
	return rootCmd
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
// the nearest command's usage. cobra has already printed "Error: <message>", so
// this appends the hint then the usage block and returns the process exit code.
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
// arg-validation failures behave like every other usage error.
// Idempotent — safe to call before each invocation.
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
	rootCmd.Version = version.Version // --version comes with it

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

	// Flag-parse failures (unknown flag, missing value, bad value) are usage
	// errors. Set on the root; cobra walks up to it for every subcommand.
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
