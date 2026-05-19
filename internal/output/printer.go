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
	// FormatName prints one identifier per line composed of CONTEXT, optional
	// NAMESPACE, and NAME columns joined by "/". Designed for pipe-into-xargs
	// scripting. When CONTEXT and NAMESPACE columns are absent the first
	// non-empty column is used.
	FormatName Format = "name"
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
	case "name":
		return FormatName, nil
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
	// NoHeaders suppresses the leading header row in table/wide output.
	// Has no effect on json/yaml/name formats.
	NoHeaders bool
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
	case FormatName:
		return printName(w, t)
	}
	return fmt.Errorf("unknown format %q", f)
}

func printTable(w io.Writer, t *Table, wide bool) error {
	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	if !t.NoHeaders {
		headers := append([]string{}, t.Headers...)
		if wide {
			headers = append(headers, t.WideHeaders...)
		}
		if _, err := fmt.Fprintln(tw, strings.Join(headers, "\t")); err != nil {
			return err
		}
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

// printName emits one identifier per line. The identifier is built from the
// CONTEXT, NAMESPACE (if non-empty), and NAME columns when present, joined
// by "/". If NAME is absent, the first non-empty regular column is used.
// Rows whose composed identifier is empty are skipped.
func printName(w io.Writer, t *Table) error {
	ctxIdx := indexOf(t.Headers, "CONTEXT")
	nsIdx := indexOf(t.Headers, "NAMESPACE")
	nameIdx := indexOf(t.Headers, "NAME")
	for _, r := range t.Rows {
		var parts []string
		if ctxIdx >= 0 && ctxIdx < len(r) && r[ctxIdx] != "" {
			parts = append(parts, r[ctxIdx])
		}
		if nsIdx >= 0 && nsIdx < len(r) && r[nsIdx] != "" {
			parts = append(parts, r[nsIdx])
		}
		switch {
		case nameIdx >= 0 && nameIdx < len(r) && r[nameIdx] != "":
			parts = append(parts, r[nameIdx])
		case len(parts) == 0:
			for _, v := range r {
				if v != "" {
					parts = append(parts, v)
					break
				}
			}
		}
		if len(parts) == 0 {
			continue
		}
		if _, err := fmt.Fprintln(w, strings.Join(parts, "/")); err != nil {
			return err
		}
	}
	return nil
}

func indexOf(headers []string, name string) int {
	for i, h := range headers {
		if h == name {
			return i
		}
	}
	return -1
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
