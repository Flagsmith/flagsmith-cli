package cmd

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/Flagsmith/flagsmith-cli/internal/output"
)

// renderList renders a list command's items: as JSON when requested, else the
// empty message when there are none, or a table of row(i, item) under headers
// with a "N noun" count footer. An empty `one` noun skips the footer. row
// receives the index so tables can join against a sibling slice built in the
// same order.
func renderList[T any](cmd *cobra.Command, items []T, empty string, headers []string, row func(int, T) []string, one, many string) error {
	return output.Render(cmd.OutOrStdout(), items, outputOpts(), func(w io.Writer) error {
		if len(items) == 0 {
			fmt.Fprintln(w, empty)
			return nil
		}
		rows := make([][]string, len(items))
		for i, it := range items {
			rows[i] = row(i, it)
		}
		if err := output.Table(w, headers, rows); err != nil {
			return err
		}
		if one != "" {
			fmt.Fprintf(w, "\n%d %s\n", len(items), plural(len(items), one, many))
		}
		return nil
	})
}
