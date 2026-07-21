package cmd

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

func formatValue(v resolved) string {
	if v.Value == nil {
		return "-"
	}
	if v.Name != "" {
		return fmt.Sprintf("%s (%v)", v.Name, v.Value)
	}
	return fmt.Sprint(v.Value)
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show the resolved context and where each value comes from",
	RunE: func(cmd *cobra.Command, args []string) error {
		pc, err := applyContext(cmd)
		if err != nil {
			return err
		}

		// config's JSON is a bespoke keyed shape (it is CLI context, not an
		// API resource); the same value feeds the human table.
		data := struct {
			ConfigPath   resolved `json:"configPath"`
			Project      resolved `json:"project"`
			Organisation resolved `json:"organisation"`
			Environment  resolved `json:"environment"`
			APIURL       resolved `json:"apiUrl"`
			SDKAPIURL    resolved `json:"sdkApiUrl"`
		}{pc.ConfigPath, pc.Project, pc.Organisation, pc.Environment, pc.APIURL, pc.SDKAPIURL}

		return output.Render(cmd.OutOrStdout(), data, outputOpts(), func(w io.Writer) error {
			tw := tabwriter.NewWriter(w, 2, 0, 3, ' ', 0)
			configPath := "-"
			if pc.ConfigPath.Value != nil {
				configPath = fmt.Sprint(pc.ConfigPath.Value)
			}
			fmt.Fprintf(tw, "Config file\t%s\t%s\n", configPath, pc.ConfigPath.Source)
			fmt.Fprintf(tw, "Project\t%s\t%s\n", formatValue(pc.Project), pc.Project.Source)
			fmt.Fprintf(tw, "Organisation\t%s\t%s\n", formatValue(pc.Organisation), pc.Organisation.Source)
			fmt.Fprintf(tw, "Environment\t%s\t%s\n", formatValue(pc.Environment), pc.Environment.Source)
			fmt.Fprintf(tw, "API\t%s\t%s\n", formatValue(pc.APIURL), pc.APIURL.Source)
			fmt.Fprintf(tw, "SDK API\t%s\t%s\n", formatValue(pc.SDKAPIURL), pc.SDKAPIURL.Source)
			return tw.Flush()
		})
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
}
