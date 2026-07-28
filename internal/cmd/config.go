package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

// abbreviateHome shortens a path under the user's home directory to a ~ prefix,
// for display only. Paths outside home are returned unchanged.
func abbreviateHome(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + p[len(home):]
	}
	return p
}

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
	Use:     "config",
	Short:   "Show the resolved context and where each value comes from",
	Example: "  flagsmith config",
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
			configPath := "-"
			if pc.ConfigPath.Value != nil {
				configPath = abbreviateHome(fmt.Sprint(pc.ConfigPath.Value))
			}
			return output.Detail(w, []output.Field{
				{Label: "Config file", Value: configPath, Source: pc.ConfigPath.Source},
				{Label: "Project", Value: formatValue(pc.Project), Source: pc.Project.Source},
				{Label: "Organisation", Value: formatValue(pc.Organisation), Source: pc.Organisation.Source},
				{Label: "Environment", Value: formatValue(pc.Environment), Source: pc.Environment.Source},
				{Label: "API", Value: formatValue(pc.APIURL), Source: pc.APIURL.Source},
				{Label: "SDK API", Value: formatValue(pc.SDKAPIURL), Source: pc.SDKAPIURL.Source},
			})
		})
	},
}

func init() {
	rootCmd.AddCommand(configCmd)
}
