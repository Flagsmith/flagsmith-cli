package cmd

import (
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var apiURL string

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
	defaultAPI := os.Getenv("FLAGSMITH_API_URL")
	if defaultAPI == "" {
		defaultAPI = "https://api.flagsmith.com"
	}
	rootCmd.PersistentFlags().StringVar(
		&apiURL, "api-url", defaultAPI,
		"Flagsmith API base URL (env: FLAGSMITH_API_URL)",
	)
	// --api is a hidden alias of --api-url.
	rootCmd.SetGlobalNormalizationFunc(func(f *pflag.FlagSet, name string) pflag.NormalizedName {
		if name == "api" {
			name = "api-url"
		}
		return pflag.NormalizedName(name)
	})
}
