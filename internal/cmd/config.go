package cmd

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"
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

		if jsonOutput() {
			out := struct {
				ConfigPath   resolved `json:"configPath"`
				Project      resolved `json:"project"`
				Organisation resolved `json:"organisation"`
				Environment  resolved `json:"environment"`
				APIURL       resolved `json:"apiUrl"`
				SDKAPIURL    resolved `json:"sdkApiUrl"`
			}{pc.ConfigPath, pc.Project, pc.Organisation, pc.Environment, pc.APIURL, pc.SDKAPIURL}
			encoded, err := json.MarshalIndent(out, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(encoded))
			return nil
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 0, 3, ' ', 0)
		configPath := "-"
		if pc.ConfigPath.Value != nil {
			configPath = fmt.Sprint(pc.ConfigPath.Value)
		}
		fmt.Fprintf(w, "Config file\t%s\t%s\n", configPath, pc.ConfigPath.Source)
		fmt.Fprintf(w, "Project\t%s\t%s\n", formatValue(pc.Project), pc.Project.Source)
		fmt.Fprintf(w, "Organisation\t%s\t%s\n", formatValue(pc.Organisation), pc.Organisation.Source)
		fmt.Fprintf(w, "Environment\t%s\t%s\n", formatValue(pc.Environment), pc.Environment.Source)
		fmt.Fprintf(w, "API\t%s\t%s\n", formatValue(pc.APIURL), pc.APIURL.Source)
		fmt.Fprintf(w, "SDK API\t%s\t%s\n", formatValue(pc.SDKAPIURL), pc.SDKAPIURL.Source)
		return w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
}
