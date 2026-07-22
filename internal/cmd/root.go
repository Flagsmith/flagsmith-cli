package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Flagsmith/flagsmith-cli/internal/auth"
	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

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
	jsonFlag         bool
	jqFlag           string
)

// jsonOutput reports whether machine-readable output was requested.
func jsonOutput() bool {
	return jsonFlag || os.Getenv("FLAGSMITH_JSON_OUTPUT") != ""
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
	if os.Getenv(envAPIKey) != "" {
		return false
	}
	if _, err := auth.Load(pc.apiURL()); !errors.Is(err, auth.ErrNotLoggedIn) {
		return false
	}
	return true
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		var usage *usageError
		if errors.As(err, &usage) {
			os.Exit(2)
		}
		os.Exit(1)
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
		"never prompt; answer confirmations affirmatively (env: FLAGSMITH_NO_INPUT)")
	flags.BoolVar(&jsonFlag, "json", false,
		"output JSON instead of human-readable text (env: FLAGSMITH_JSON_OUTPUT)")
	flags.StringVar(&jqFlag, "jq", "",
		"filter JSON output through a jq expression (implies --json)")

	rootCmd.SetUsageTemplate(singleLineUsage(rootCmd.UsageTemplate()))

	// Hidden aliases: --api for --api-url, --no-input for --yes.
	rootCmd.SetGlobalNormalizationFunc(func(f *pflag.FlagSet, name string) pflag.NormalizedName {
		switch name {
		case "api":
			name = "api-url"
		case "no-input":
			name = "yes"
		}
		return pflag.NormalizedName(name)
	})
}
