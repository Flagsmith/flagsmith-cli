package cmd

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/api"
	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

var flagCmd = &cobra.Command{
	Use:   "flag",
	Short: "Inspect feature flags in the current environment",
}

func flagValue(v any) string {
	if v == nil {
		return "-"
	}
	return fmt.Sprint(v)
}

var flagListCmd = &cobra.Command{
	Use:   "list",
	Short: "List every flag in the current environment",
	RunE: func(cmd *cobra.Command, args []string) error {
		pc, err := applyContext(cmd)
		if err != nil {
			return err
		}
		envKey, err := resolveEnvironmentKey(cmd, pc)
		if err != nil {
			return err
		}
		flags, err := api.Flags(cmd.Context(), pc.SDKAPIURL.Value.(string), envKey)
		if err != nil {
			return err
		}
		return output.Render(cmd.OutOrStdout(), flags, outputOpts(), func(w io.Writer) error {
			if len(flags) == 0 {
				fmt.Fprintln(w, "No flags.")
				return nil
			}
			rows := make([][]string, len(flags))
			for i, f := range flags {
				rows[i] = []string{f.Feature.Name, strconv.FormatBool(f.Enabled), flagValue(f.Value)}
			}
			if err := output.Table(w, []string{"NAME", "ENABLED", "VALUE"}, rows); err != nil {
				return err
			}
			fmt.Fprintf(w, "\n%d %s\n", len(flags), plural(len(flags), "flag", "flags"))
			return nil
		})
	},
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func init() {
	flagCmd.AddCommand(flagListCmd)
	rootCmd.AddCommand(flagCmd)
}
