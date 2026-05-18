// Package output renders command results as table, JSON, or YAML.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"sigs.k8s.io/yaml"
)

// Format is the wire form for `-o`.
type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
	FormatWide  Format = "wide"
)

// ParseFormat returns Format for a `-o` string. Empty defaults to table.
func ParseFormat(s string) (Format, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "table":
		return FormatTable, nil
	case "wide":
		return FormatWide, nil
	case "json":
		return FormatJSON, nil
	case "yaml":
		return FormatYAML, nil
	default:
		return "", fmt.Errorf("unknown output format %q", s)
	}
}

// Table is a tabular result. Headers + Rows have matching column count.
// WideExtra columns appear only when Format==FormatWide.
type Table struct {
	Headers     []string
	Rows        [][]string
	WideHeaders []string
	WideRows    [][]string
}

// Append a row. wide may be nil if the table has no wide columns.
func (t *Table) Append(row []string, wide []string) {
	t.Rows = append(t.Rows, row)
	if len(t.WideHeaders) > 0 {
		t.WideRows = append(t.WideRows, wide)
	}
}

// Print renders t to w in the requested format. For JSON/YAML the rows are
// serialised as a slice of objects keyed by header (wide columns merged in
// when applicable).
func Print(w io.Writer, t *Table, f Format) error {
	switch f {
	case FormatTable, FormatWide:
		return printTable(w, t, f == FormatWide)
	case FormatJSON:
		return printJSON(w, toObjects(t, true))
	case FormatYAML:
		return printYAML(w, toObjects(t, true))
	}
	return fmt.Errorf("unknown format %q", f)
}

func printTable(w io.Writer, t *Table, wide bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	headers := append([]string{}, t.Headers...)
	if wide {
		headers = append(headers, t.WideHeaders...)
	}
	if _, err := fmt.Fprintln(tw, strings.Join(headers, "\t")); err != nil {
		return err
	}
	for i, r := range t.Rows {
		cols := append([]string{}, r...)
		if wide && i < len(t.WideRows) {
			cols = append(cols, t.WideRows[i]...)
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cols, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func toObjects(t *Table, includeWide bool) []map[string]string {
	out := make([]map[string]string, 0, len(t.Rows))
	for i, r := range t.Rows {
		m := map[string]string{}
		for j, h := range t.Headers {
			if j < len(r) {
				m[h] = r[j]
			}
		}
		if includeWide && i < len(t.WideRows) {
			for j, h := range t.WideHeaders {
				if j < len(t.WideRows[i]) {
					m[h] = t.WideRows[i][j]
				}
			}
		}
		out = append(out, m)
	}
	return out
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printYAML(w io.Writer, v any) error {
	b, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal yaml: %w", err)
	}
	_, err = w.Write(b)
	return err
}
