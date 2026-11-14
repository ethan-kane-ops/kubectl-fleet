package output

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseFormatMarkup(t *testing.T) {
	for in, want := range map[string]Format{"md": FormatMarkdown, "markdown": FormatMarkdown, "html": FormatHTML} {
		got, err := ParseFormat(in)
		if err != nil || got != want {
			t.Errorf("ParseFormat(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func markupTable() *Table {
	t := &Table{Headers: []string{"CONTEXT", "NAME"}, WideHeaders: []string{"CLUSTER"}}
	t.Append([]string{"a|b", "x"}, []string{"wide-only"})
	return t
}

func TestPrintMarkdown(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, markupTable(), FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	want := "| CONTEXT | NAME |\n| --- | --- |\n| a\\|b | x |\n"
	if buf.String() != want {
		t.Errorf("got:\n%s\nwant:\n%s", buf.String(), want)
	}

	tbl := markupTable()
	tbl.NoHeaders = true
	buf.Reset()
	if err := Print(&buf, tbl, FormatMarkdown); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "| a\\|b | x |\n" {
		t.Errorf("no-headers got %q", buf.String())
	}
}

func TestPrintHTML(t *testing.T) {
	tbl := &Table{Headers: []string{"CONTEXT"}, NoHeaders: true}
	tbl.Append([]string{"<b>&"}, nil)
	var buf bytes.Buffer
	if err := Print(&buf, tbl, FormatHTML); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{"<!doctype html>", "<style>", "<th>CONTEXT</th>", "&lt;b&gt;&amp;"} {
		if !strings.Contains(out, want) {
			t.Errorf("html lacks %q:\n%s", want, out)
		}
	}
	if !strings.HasPrefix(out, "<!doctype html>") || strings.Contains(out, "<script") {
		t.Errorf("want a standalone document without scripts:\n%s", out)
	}
}

func TestMergeWide(t *testing.T) {
	tbl := markupTable()
	tbl.MergeWide()
	if strings.Join(tbl.Headers, ",") != "CONTEXT,NAME,CLUSTER" || len(tbl.WideHeaders) != 0 || tbl.Rows[0][2] != "wide-only" {
		t.Errorf("MergeWide: %+v", tbl)
	}
}
