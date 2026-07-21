// Package output centralises how commands render results: JSON (optionally
// filtered with a jq expression) for scripts, human tables/detail views
// otherwise. The same data value feeds both, so the two never drift.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/itchyny/gojq"
)

// Options selects the output format for a single command invocation.
type Options struct {
	JSON bool   // emit JSON instead of a human view
	JQ   string // filter JSON through this jq expression (implies JSON)
}

func (o Options) jsonMode() bool { return o.JSON || o.JQ != "" }

var (
	headerColor = color.New(color.Bold)
	labelColor  = color.New(color.FgCyan)
	tickColor   = color.New(color.FgGreen, color.Bold)
)

// Render writes data as JSON (applying JQ) when JSON output is requested,
// otherwise delegates to human. human may be nil when only JSON is expected.
func Render(w io.Writer, data any, opts Options, human func(io.Writer) error) error {
	if !opts.jsonMode() {
		if human == nil {
			return nil
		}
		return human(w)
	}
	if opts.JQ != "" {
		return renderJQ(w, data, opts.JQ)
	}
	encoded, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(encoded))
	return err
}

// renderJQ runs a jq expression over data and prints each result: raw for
// strings, compact JSON otherwise (matching `gh --jq`).
func renderJQ(w io.Writer, data any, expr string) error {
	query, err := gojq.Parse(expr)
	if err != nil {
		return fmt.Errorf("invalid jq expression: %w", err)
	}
	// Round-trip through JSON so jq sees plain maps/slices, not Go structs.
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	var input any
	if err := json.Unmarshal(raw, &input); err != nil {
		return err
	}
	iter := query.Run(input)
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := v.(error); ok {
			return err
		}
		if s, ok := v.(string); ok {
			fmt.Fprintln(w, s)
			continue
		}
		encoded, err := json.Marshal(v)
		if err != nil {
			return err
		}
		fmt.Fprintln(w, string(encoded))
	}
	return nil
}

// Table writes a borderless, aligned table with bold headers.
func Table(w io.Writer, headers []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 2, 0, 3, ' ', 0)
	fmt.Fprintln(tw, joinTabs(headers, headerColor.Sprint))
	for _, row := range rows {
		fmt.Fprintln(tw, joinTabs(row, nil))
	}
	return tw.Flush()
}

// Field is one label/value pair in a detail view.
type Field struct {
	Label string
	Value string
}

// Detail writes an aligned key/value view of a single resource.
func Detail(w io.Writer, fields []Field) error {
	tw := tabwriter.NewWriter(w, 2, 0, 3, ' ', 0)
	for _, f := range fields {
		fmt.Fprintf(tw, "%s\t%s\n", labelColor.Sprint(f.Label), f.Value)
	}
	return tw.Flush()
}

// Success writes a ✓-prefixed confirmation line (callers pass stderr).
func Success(w io.Writer, format string, a ...any) {
	fmt.Fprintf(w, "%s %s\n", tickColor.Sprint("✓"), fmt.Sprintf(format, a...))
}

func joinTabs(cells []string, style func(...any) string) string {
	out := ""
	for i, c := range cells {
		if i > 0 {
			out += "\t"
		}
		if style != nil {
			out += style(c)
		} else {
			out += c
		}
	}
	return out
}
