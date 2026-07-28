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

// evalView is the curated JSON/detail shape for one resolved flag: what an SDK
// hands the application, and nothing else. Human output and JSON stay in
// lockstep.
type evalView struct {
	Feature string `json:"feature"`
	Enabled bool   `json:"enabled"`
	Value   any    `json:"value"`
}

func newEvalView(f flagsmith.Flag) evalView {
	return evalView{Feature: f.FeatureName, Enabled: f.Enabled, Value: f.Value}
}

var (
	evalIdentityFlag string
	evalTraitFlags   []string
	evalPersistFlag  bool
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
  flagsmith eval onboarding --identity user-123 --jq .value`,
	Args: cobra.MaximumNArgs(1),
	RunE: runEvaluate,
}

func runEvaluate(cmd *cobra.Command, args []string) error {
	if evalPersistFlag && evalIdentityFlag == "" {
		return usageErrorf("there is no identity to persist — pass --identity, or drop --persist")
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
		return renderEvalDetail(cmd, newEvalView(*flag))
	}
	views := make([]evalView, len(all))
	for i, flag := range all {
		views[i] = newEvalView(flag)
	}
	return renderList(cmd, views, "No flags.",
		[]string{"FEATURE", "ENABLED", "VALUE"},
		func(_ int, v evalView) []string {
			return []string{v.Feature, boolState(v.Enabled), truncateValue(valueDisplay(v.Value))}
		}, "flag", "flags")
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

// renderEvalDetail prints one resolved flag's detail view (or its JSON).
func renderEvalDetail(cmd *cobra.Command, v evalView) error {
	return output.Render(cmd.OutOrStdout(), v, outputOpts(), func(w io.Writer) error {
		return output.Detail(w, []output.Field{
			{Label: "Feature", Value: v.Feature},
			{Label: "Enabled", Value: boolState(v.Enabled)},
			{Label: "Value", Value: valueDisplay(v.Value)},
		})
	})
}

func init() {
	f := evaluateCmd.Flags()
	f.StringVar(&evalIdentityFlag, "identity", "", "resolve for this identifier")
	f.StringArrayVar(&evalTraitFlags, "trait", nil, "trait key=value to evaluate with (repeatable)")
	f.BoolVar(&evalPersistFlag, "persist", false, "persist the identity and its traits instead of evaluating transiently")
	rootCmd.AddCommand(evaluateCmd)
}
