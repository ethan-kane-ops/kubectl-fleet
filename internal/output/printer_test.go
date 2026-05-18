package output

import (
	"bytes"
	"strings"
	"testing"
)

func sampleTable() *Table {
	t := &Table{
		Headers:     []string{"CONTEXT", "NAME"},
		WideHeaders: []string{"CLUSTER"},
	}
	t.Append([]string{"prod", "a"}, []string{"c-prod"})
	t.Append([]string{"dev", "b"}, []string{"c-dev"})
	return t
}

func TestParseFormat(t *testing.T) {
	cases := map[string]Format{
		"":      FormatTable,
		"table": FormatTable,
		"JSON":  FormatJSON,
		"yaml":  FormatYAML,
		"wide":  FormatWide,
	}
	for in, want := range cases {
		got, err := ParseFormat(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("%q -> %q want %q", in, got, want)
		}
	}
	if _, err := ParseFormat("xml"); err == nil {
		t.Error("expected error for unknown format")
	}
}

func TestPrintTable(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, sampleTable(), FormatTable); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "CONTEXT") || !strings.Contains(out, "NAME") {
		t.Errorf("missing headers: %q", out)
	}
	if !strings.Contains(out, "prod") || !strings.Contains(out, "dev") {
		t.Errorf("missing rows: %q", out)
	}
	if strings.Contains(out, "CLUSTER") {
		t.Errorf("wide column leaked into table output: %q", out)
	}
}

func TestPrintWide(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, sampleTable(), FormatWide); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "CLUSTER") || !strings.Contains(out, "c-prod") {
		t.Errorf("wide missing: %q", out)
	}
}

func TestPrintJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, sampleTable(), FormatJSON); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"CONTEXT": "prod"`) || !strings.Contains(out, `"CLUSTER": "c-dev"`) {
		t.Errorf("json missing keys: %q", out)
	}
}

func TestPrintYAML(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, sampleTable(), FormatYAML); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "CONTEXT: prod") {
		t.Errorf("yaml missing key: %q", out)
	}
}

func TestPrintEmpty(t *testing.T) {
	var buf bytes.Buffer
	tbl := &Table{Headers: []string{"A", "B"}}
	if err := Print(&buf, tbl, FormatTable); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if !strings.Contains(buf.String(), "A") {
		t.Errorf("headers missing on empty table: %q", buf.String())
	}
}
