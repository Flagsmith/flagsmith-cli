package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// apiURL is the resolved instance URL for the current invocation, set by
// applyContext. Flag values live in the *Flag variables below.
var apiURL string

var (
	apiURLFlag       string
	sdkAPIURLFlag    string
	projectFlag      int
	organisationFlag int
	environmentFlag  string
	configPathFlag   string
)

var rootCmd = &cobra.Command{
	Use:          "flagsmith",
	Short:        "The Flagsmith command-line interface",
	SilenceUsage: true,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	flags := rootCmd.PersistentFlags()
	flags.StringVar(&apiURLFlag, "api-url", defaultAPIURL,
		"Flagsmith API base URL (env: FLAGSMITH_API_URL)")
	flags.StringVar(&sdkAPIURLFlag, "sdk-api-url", "",
		"SDK API base URL for flag evaluation (env: FLAGSMITH_SDK_API_URL)")
	flags.IntVarP(&projectFlag, "project", "p", 0,
		"project ID (env: FLAGSMITH_PROJECT)")
	flags.IntVar(&organisationFlag, "organisation", 0,
		"organisation ID (env: FLAGSMITH_ORGANISATION)")
	flags.StringVarP(&environmentFlag, "environment", "e", "",
		"environment as its client-side SDK key (env: FLAGSMITH_ENVIRONMENT)")
	flags.StringVarP(&configPathFlag, "config-path", "c", "",
		"path to flagsmith.json (env: FLAGSMITH_CONFIG_PATH)")

	// --api is a hidden alias of --api-url.
	rootCmd.SetGlobalNormalizationFunc(func(f *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "api" {
			name = "api-url"
		}
		return pflag.NormalizedName(name)
	})
}
