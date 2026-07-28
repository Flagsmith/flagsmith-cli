// Package output centralises how commands render results: JSON (optionally
// filtered with a jq expression) for scripts, human tables/detail views
// otherwise. The same data value feeds both, so the two never drift.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
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

var tickColor = color.New(color.FgGreen, color.Bold)

// SGR codes applied after tabwriter alignment (see paint).
const (
	sgrBold  = "1"
	sgrFaint = "2"
	sgrCyan  = "36"
)

// paint wraps s in an ANSI SGR sequence, or returns it unchanged when colour
// is disabled. It is applied to already-aligned tabwriter output — never to a
// cell before alignment — so the escape bytes cannot affect column widths.
func paint(code, s string) string {
	if color.NoColor {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

// splitLines splits tabwriter output into its lines, dropping the trailing
// newline so callers can restyle and rejoin without adding a blank line.
func splitLines(s string) []string {
	return strings.Split(strings.TrimRight(s, "\n"), "\n")
}

// flattenCell makes a value safe to hand to tabwriter, which reads all four
// of \t \v \n \f as structure: a newline ends the row — breaking the alignment of
// every row after it, and desynchronising output lines from input rows — and
// a tab starts a new cell. Values reach us unsanitised from the API, so they
// are flattened to spaces here rather than at each call site. Runs of plain
// spaces are left alone: they are content, not structure.
var flattenCell = strings.NewReplacer(
	"\r\n", " ", "\n", " ", "\r", " ", "\t", " ", "\v", " ", "\f", " ",
).Replace

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

// Table writes a borderless, aligned table with a bold header. tabwriter
// aligns on the plain text first; the header is bolded afterwards so its
// escape bytes never count toward column width.
func Table(w io.Writer, headers []string, rows [][]string) error {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 2, 0, 3, ' ', 0)
	// Flatten each cell before joining: the tabs between them are the
	// structure tabwriter aligns on.
	writeRow := func(cells []string) {
		flat := make([]string, len(cells))
		for i, c := range cells {
			flat[i] = flattenCell(c)
		}
		fmt.Fprintln(tw, strings.Join(flat, "\t"))
	}
	writeRow(headers)
	for _, row := range rows {
		writeRow(row)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	lines := splitLines(buf.String())
	lines[0] = paint(sgrBold, lines[0])
	_, err := fmt.Fprintln(w, strings.Join(lines, "\n"))
	return err
}

// Field is one row in a detail view: a label, a value, and an optional
// source (e.g. where a config value came from). When any field has a
// source, Detail renders a third, dimmed column.
type Field struct {
	Label  string
	Value  string
	Source string
}

// Detail writes an aligned key/value view of a single resource. Labels are
// coloured and, when present, sources dimmed. As with Table, alignment
// happens on the plain text first and colour is applied afterwards, so the
// escape bytes never affect column widths.
func Detail(w io.Writer, fields []Field) error {
	hasSource := false
	for _, f := range fields {
		if f.Source != "" {
			hasSource = true
		}
	}
	// Flatten into a copy: the colourer matches against what was written, and
	// the caller's slice is not ours to modify.
	flat := make([]Field, len(fields))
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 2, 0, 3, ' ', 0)
	for i, f := range fields {
		flat[i] = Field{Label: flattenCell(f.Label), Value: flattenCell(f.Value), Source: flattenCell(f.Source)}
		if hasSource {
			fmt.Fprintf(tw, "%s\t%s\t%s\n", flat[i].Label, flat[i].Value, flat[i].Source)
		} else {
			fmt.Fprintf(tw, "%s\t%s\n", flat[i].Label, flat[i].Value)
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	// One row in, one line out — guaranteed by the flattening above.
	for i, line := range splitLines(buf.String()) {
		fmt.Fprintln(w, colorDetailLine(line, flat[i]))
	}
	return nil
}

// colorDetailLine colours an already-aligned detail line: the label cyan
// and, when the row has one, the source dim.
func colorDetailLine(line string, f Field) string {
	if color.NoColor {
		return line
	}
	if f.Source != "" && strings.HasSuffix(line, f.Source) {
		cut := len(line) - len(f.Source)
		line = line[:cut] + paint(sgrFaint, line[cut:])
	}
	if f.Label != "" && strings.HasPrefix(line, f.Label) {
		line = paint(sgrCyan, f.Label) + line[len(f.Label):]
	}
	return line
}

// Success writes a ✓-prefixed confirmation line (callers pass stderr).
func Success(w io.Writer, format string, a ...any) {
	fmt.Fprintf(w, "%s %s\n", tickColor.Sprint("✓"), fmt.Sprintf(format, a...))
}
