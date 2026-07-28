package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	flagsmith "github.com/Flagsmith/flagsmith-go-client/v5"
	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/bug"
	"github.com/Flagsmith/flagsmith-cli/internal/httpx"
	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

// evalView is one resolved flag as the human view shows it — what an SDK hands
// the application, and nothing else.
type evalView struct {
	Feature string
	Enabled bool
	Value   any
}

func newEvalView(f flagsmith.Flag) evalView {
	return evalView{Feature: f.FeatureName, Enabled: f.Enabled, Value: f.Value}
}

const evaluationResultSchema = "https://raw.githubusercontent.com/Flagsmith/flagsmith/main/sdk/evaluation-result.json"

// evalResult is `--json`: an EvaluationResult, as much of it as a remote
// evaluation can fill.
type evalResult struct {
	Schema string              `json:"$schema"`
	Flags  map[string]evalFlag `json:"flags"`
}

// evalFlag is one flag in an EvaluationResult, and is also what naming a feature
// prints on its own.
type evalFlag struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Value   any    `json:"value"`
}

func newEvalFlag(v evalView) evalFlag {
	return evalFlag{Name: v.Feature, Enabled: v.Enabled, Value: v.Value}
}

func newEvalResult(views []evalView) evalResult {
	flags := make(map[string]evalFlag, len(views))
	for _, v := range views {
		flags[v.Feature] = newEvalFlag(v)
	}
	return evalResult{Schema: evaluationResultSchema, Flags: flags}
}

// sdkState is `--js`: the state a Flagsmith frontend SDK hydrates from.
type sdkState struct {
	API   string            `json:"api"`
	Flags map[string]jsFlag `json:"flags"`
}

// jsFlag is one flag in the hydration state.
type jsFlag struct {
	Enabled bool `json:"enabled"`
	Value   any  `json:"value"`
}

// newSDKState keys flags by feature name, as the frontend SDKs look them up.
func newSDKState(sdkURL string, views []evalView) sdkState {
	flags := make(map[string]jsFlag, len(views))
	for _, v := range views {
		flags[v.Feature] = jsFlag{Enabled: v.Enabled, Value: v.Value}
	}
	return sdkState{API: sdkAPIBase(sdkURL), Flags: flags}
}

// sdkAPIBase is the SDK API base URL as the SDKs write it, trailing slash and all.
func sdkAPIBase(sdkURL string) string {
	return strings.TrimRight(sdkURL, "/") + "/api/v1/"
}

var (
	evalIdentityFlag string
	evalTraitFlags   []string
	evalPersistFlag  bool
	evalJSFlag       bool
)

var evaluateCmd = &cobra.Command{
	Use:     "evaluate [feature]",
	Aliases: []string{"eval"},
	Short:   "Show the flags an SDK resolves for the current environment",
	Example: `  # the flags a fresh SDK sees
  flagsmith evaluate

  # resolved for one user: segment overrides applied, variants allocated
  flagsmith evaluate --identity user-123

  # a what-if: overlay traits without persisting the identity
  flagsmith evaluate --identity user-123 --trait plan=premium --trait age=42

  # one feature, for scripting
  flagsmith eval onboarding --identity user-123 --jq .value

  # bootstrap a frontend SDK (can be provided as state)
  flagsmith eval --js > state.json`,
	Args: cobra.MaximumNArgs(1),
	RunE: runEvaluate,
}

func runEvaluate(cmd *cobra.Command, args []string) error {
	if evalPersistFlag && evalIdentityFlag == "" {
		return usageErrorf("there is no identity to persist — pass --identity, or drop --persist")
	}
	if evalJSFlag && jsonFlag {
		return usageErrorf("--js and --json are different shapes — pass one or the other")
	}
	// --js takes the environment whole or not at all.
	if evalJSFlag && len(args) == 1 {
		return usageErrorf("--js describes a whole environment — drop %q, or drop --js", args[0])
	}
	traits, err := parseTraits(evalTraitFlags)
	if err != nil {
		return err
	}
	pc, err := applyContext(cmd)
	if err != nil {
		return err
	}
	key, err := sdkEnvironmentKey(cmd, pc)
	if err != nil {
		return err
	}
	sdkURL, _ := pc.SDKAPIURL.Value.(string)

	flags, err := evaluateFlags(cmd.Context(), sdkURL, key, evalIdentityFlag, traits, evalPersistFlag)
	if err != nil {
		return evalError(sdkURL, err)
	}
	all := flags.AllFlags()

	if len(args) == 1 {
		flag := findEvaluated(all, args[0])
		if flag == nil {
			return withHint(fmt.Errorf("no flag named %q was resolved", args[0]), hintEvaluate)
		}
		return renderEvaluation(cmd, sdkURL, []evalView{newEvalView(*flag)}, true)
	}
	views := make([]evalView, len(all))
	for i, flag := range all {
		views[i] = newEvalView(flag)
	}
	return renderEvaluation(cmd, sdkURL, views, false)
}

// renderEvaluation writes the resolved flags: an EvaluationResult, or the
// frontend SDK's hydration state for --js, or the human table.
func renderEvaluation(cmd *cobra.Command, sdkURL string, views []evalView, single bool) error {
	var doc any = newEvalResult(views)
	if single {
		doc = newEvalFlag(views[0])
	}
	if evalJSFlag {
		doc = newSDKState(sdkURL, views)
		// An SDK handed a state with no flags has nothing to hydrate and waits
		// for a fetch a serverState-only app never makes. The file is still
		// written: with options of its own, an SDK fetches and is fine.
		if len(views) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(),
				"Warning: no flags to hydrate from — an SDK given this state will wait for a fetch")
		}
	}
	opts := outputOpts()
	opts.JSON = opts.JSON || evalJSFlag
	return output.Render(cmd.OutOrStdout(), doc, opts, func(w io.Writer) error {
		if single {
			v := views[0]
			return output.Detail(w, []output.Field{
				{Label: "Feature", Value: v.Feature},
				{Label: "Enabled", Value: boolState(v.Enabled)},
				{Label: "Value", Value: valueDisplay(v.Value)},
			})
		}
		if len(views) == 0 {
			fmt.Fprintln(w, "No flags.")
			return nil
		}
		rows := make([][]string, len(views))
		for i, v := range views {
			rows[i] = []string{v.Feature, boolState(v.Enabled), truncateValue(valueDisplay(v.Value))}
		}
		if err := output.Table(w, []string{"FEATURE", "ENABLED", "VALUE"}, rows); err != nil {
			return err
		}
		fmt.Fprintf(w, "\n%d %s\n", len(views), plural(len(views), "flag", "flags"))
		return nil
	})
}

// evaluateFlags resolves the environment's flags over the SDK API: the
// environment defaults on their own, or an identity evaluation as soon as an
// identity or a trait is in play. An empty identifier is the anonymous identity
// the SDK API evaluates without ever storing.
//
// The evaluation is transient unless --persist was given, so a what-if leaves
// neither the identity nor its traits behind. The SDK reads that choice from the
// context, and the environment key it was built with authenticates the call.
func evaluateFlags(ctx context.Context, sdkURL, key, identity string, traits []*flagsmith.Trait, persist bool) (flagsmith.Flags, error) {
	client := newSDKClient(sdkURL, key)
	if identity == "" && len(traits) == 0 {
		return client.GetEnvironmentFlagsFromAPI(ctx)
	}
	transient := !persist
	ctx = flagsmith.WithEvaluationContext(ctx, flagsmith.EvaluationContext{
		Identity: &flagsmith.IdentityEvaluationContext{Transient: &transient},
	})
	return client.GetIdentityFlagsFromAPI(ctx, identity, traits)
}

// newSDKClient builds the Flagsmith SDK client for the resolved SDK surface.
//
// Evaluation goes over the CLI's own transport.
// The SDK's own logger is discarded.
//
// The http.Client is this invocation's alone rather than the shared one, because
// NewClient installs a 10s Timeout on any client that carries none — a cap that
// would otherwise leak into every other request in the process. Clearing it
// again leaves the request context as the only bound, as everywhere else, so
// FLAGSMITH_TIMEOUT still has the last word.
func newSDKClient(sdkURL, key string) *flagsmith.Client {
	hc := httpx.New(userAgent())
	client := flagsmith.NewClient(key,
		flagsmith.WithBaseURL(strings.TrimRight(sdkURL, "/")+"/api/v1/"),
		flagsmith.WithHTTPClient(hc),
		flagsmith.WithSlogLogger(slog.New(slog.DiscardHandler)),
	)
	hc.Timeout = 0
	return client
}

// findEvaluated returns the resolved flag for the named feature, or nil. The SDK
// API answers with every flag at once, so one feature is picked out here — by
// exact name, case-insensitively, as feature references are matched elsewhere.
func findEvaluated(flags []flagsmith.Flag, ref string) *flagsmith.Flag {
	for i := range flags {
		if strings.EqualFold(flags[i].FeatureName, ref) {
			return &flags[i]
		}
	}
	return nil
}

// parseTraits turns repeated --trait key=value flags into SDK traits. Values are
// typed by the same inference as `flagsmith api -F`: true/false, a number, else
// a string.
func parseTraits(raw []string) ([]*flagsmith.Trait, error) {
	traits := make([]*flagsmith.Trait, 0, len(raw))
	for _, item := range raw {
		k, v, ok := strings.Cut(item, "=")
		if !ok {
			return nil, usageErrorf("invalid trait %q (want key=value)", item)
		}
		traits = append(traits, &flagsmith.Trait{TraitKey: k, TraitValue: typedFieldValue(v)})
	}
	return traits, nil
}

// evalError restates an SDK failure in the CLI's own terms: the SDK's errors
// carry a "flagsmith:" prefix and stack their own wording, and its message is
// all that survives of the response — never the key it was sent with, which may
// be a secret.
func evalError(sdkURL string, err error) error {
	var apiErr *flagsmith.FlagsmithAPIError
	if !errors.As(err, &apiErr) {
		return err
	}
	switch apiErr.ResponseStatusCode {
	case 0:
		// No response at all: a transport failure, whose own error is the useful
		// one — it is what says "context deadline exceeded" or "no such host".
		if apiErr.Err != nil {
			return fmt.Errorf("evaluating flags on %s: %w", sdkURL, apiErr.Err)
		}
		return bug.Mark(fmt.Errorf("evaluating flags on %s failed", sdkURL))
	case http.StatusUnauthorized, http.StatusForbidden:
		return withHint(fmt.Errorf("%s rejected the environment key (%s)", sdkURL, apiErr.ResponseStatus),
			hintEnvironmentKey)
	case http.StatusNotFound:
		return withHint(fmt.Errorf("%s has no SDK API to evaluate against (%s)", sdkURL, apiErr.ResponseStatus),
			hintSDKAPIURL)
	}
	return bug.Mark(fmt.Errorf("evaluating flags on %s returned %s", sdkURL, apiErr.ResponseStatus))
}

func init() {
	f := evaluateCmd.Flags()
	f.StringVar(&evalIdentityFlag, "identity", "", "resolve for this identifier")
	f.StringArrayVar(&evalTraitFlags, "trait", nil, "trait key=value to evaluate with (repeatable)")
	f.BoolVar(&evalPersistFlag, "persist", false, "persist the identity and its traits instead of evaluating transiently")
	f.BoolVar(&evalJSFlag, "js", false, "output the state a Flagsmith frontend SDK hydrates from")
	rootCmd.AddCommand(evaluateCmd)
}
