package cmd

import (
	"os"

	"github.com/spf13/cobra"
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
		&apiURL, "api", defaultAPI,
		"Flagsmith API base URL (env: FLAGSMITH_API_URL)",
	)
}
