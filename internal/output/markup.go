package output

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"strings"
)

// Stylesheet is the inline CSS of every HTML document kubectl-fleet writes,
// so a saved page opens anywhere with no network access.
//
//go:embed style.css
var Stylesheet string

// MergeWide moves the wide columns into the regular ones, for formats that
// have no separate wide form.
func (t *Table) MergeWide() {
	t.Headers = append(t.Headers, t.WideHeaders...)
	for i := range t.Rows {
		if i < len(t.WideRows) {
			t.Rows[i] = append(t.Rows[i], t.WideRows[i]...)
		}
	}
	t.WideHeaders, t.WideRows = nil, nil
}

// MarkdownCell escapes a value for a Markdown table cell.
func MarkdownCell(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", " ")
}

func printMarkdown(w io.Writer, t *Table) error {
	var b strings.Builder
	row := func(cells []string) {
		b.WriteString("|")
		for _, c := range cells {
			b.WriteString(" " + MarkdownCell(c) + " |")
		}
		b.WriteString("\n")
	}
	if !t.NoHeaders {
		row(t.Headers)
		sep := make([]string, len(t.Headers))
		for i := range sep {
			sep[i] = "---"
		}
		b.WriteString("| " + strings.Join(sep, " | ") + " |\n")
	}
	for _, r := range t.Rows {
		row(r)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

var htmlTable = template.Must(template.New("table").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>kubectl fleet</title>
<style>{{.CSS}}</style>
</head>
<body>
<main>
<div class="scroll">
<table>
<thead><tr>{{range .Headers}}<th>{{.}}</th>{{end}}</tr></thead>
<tbody>
{{- range .Rows}}
<tr>{{range .}}<td>{{.}}</td>{{end}}</tr>
{{- end}}
</tbody>
</table>
</div>
</main>
</body>
</html>
`))

// printHTML writes t as a standalone HTML page. It always has a header row:
// a table without one is unreadable in a browser.
func printHTML(w io.Writer, t *Table) error {
	if err := htmlTable.Execute(w, struct {
		CSS     template.CSS
		Headers []string
		Rows    [][]string
	}{template.CSS(Stylesheet), t.Headers, t.Rows}); err != nil {
		return fmt.Errorf("render html: %w", err)
	}
	return nil
}
