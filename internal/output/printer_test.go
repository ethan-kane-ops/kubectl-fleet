package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

func TestParseFormatName(t *testing.T) {
	got, err := ParseFormat("name")
	if err != nil {
		t.Fatalf("name: %v", err)
	}
	if got != FormatName {
		t.Errorf("name parsed to %q want %q", got, FormatName)
	}
}

func TestPrintName_contextNamespaceName(t *testing.T) {
	tbl := &Table{Headers: []string{"CONTEXT", "NAMESPACE", "NAME", "AGE"}}
	tbl.Append([]string{"prod", "payments", "api-1", "5h"}, nil)
	tbl.Append([]string{"prod", "payments", "db-1", "5h"}, nil)
	tbl.Append([]string{"dev", "default", "foo", "1m"}, nil)
	var buf bytes.Buffer
	if err := Print(&buf, tbl, FormatName); err != nil {
		t.Fatalf("Print: %v", err)
	}
	want := "prod/payments/api-1\nprod/payments/db-1\ndev/default/foo\n"
	if buf.String() != want {
		t.Errorf("name output =\n%qwant\n%q", buf.String(), want)
	}
}

func TestPrintName_clusterScopedNoNamespace(t *testing.T) {
	tbl := &Table{Headers: []string{"CONTEXT", "NAME", "STATUS"}}
	tbl.Append([]string{"prod", "node-1", "Ready"}, nil)
	tbl.Append([]string{"dev", "node-2", "NotReady"}, nil)
	var buf bytes.Buffer
	if err := Print(&buf, tbl, FormatName); err != nil {
		t.Fatalf("Print: %v", err)
	}
	want := "prod/node-1\ndev/node-2\n"
	if buf.String() != want {
		t.Errorf("name output =\n%qwant\n%q", buf.String(), want)
	}
}

func TestPrintName_skipEmptyNamespace(t *testing.T) {
	tbl := &Table{Headers: []string{"CONTEXT", "NAMESPACE", "NAME"}}
	tbl.Append([]string{"prod", "", "cluster-role-x"}, nil)
	tbl.Append([]string{"prod", "default", "pod-y"}, nil)
	var buf bytes.Buffer
	if err := Print(&buf, tbl, FormatName); err != nil {
		t.Fatalf("Print: %v", err)
	}
	want := "prod/cluster-role-x\nprod/default/pod-y\n"
	if buf.String() != want {
		t.Errorf("name output =\n%qwant\n%q", buf.String(), want)
	}
}

func TestPrintTable_NoHeaders(t *testing.T) {
	tbl := sampleTable()
	tbl.NoHeaders = true
	var buf bytes.Buffer
	if err := Print(&buf, tbl, FormatTable); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "CONTEXT") || strings.Contains(out, "NAME") {
		t.Errorf("headers should be suppressed, got: %q", out)
	}
	if !strings.Contains(out, "prod") || !strings.Contains(out, "dev") {
		t.Errorf("rows missing: %q", out)
	}
}

func TestPrintWide_NoHeaders(t *testing.T) {
	tbl := sampleTable()
	tbl.NoHeaders = true
	var buf bytes.Buffer
	if err := Print(&buf, tbl, FormatWide); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "CLUSTER") {
		t.Errorf("wide headers should be suppressed, got: %q", out)
	}
	if !strings.Contains(out, "c-prod") {
		t.Errorf("wide row missing: %q", out)
	}
}

func TestEncode(t *testing.T) {
	v := []struct {
		A int `json:"a"`
	}{{1}}
	cases := []struct {
		f       Format
		want    string
		wantErr bool
	}{
		{FormatJSON, "[\n  {\n    \"a\": 1\n  }\n]\n", false},
		{FormatYAML, "- a: 1\n", false},
		{FormatTable, "", true},
	}
	for _, tc := range cases {
		t.Run(string(tc.f), func(t *testing.T) {
			var buf bytes.Buffer
			err := Encode(&buf, v, tc.f)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if buf.String() != tc.want {
				t.Errorf("got %q want %q", buf.String(), tc.want)
			}
		})
	}
}

// TestEncodeListMatchesWholeListEncoding pins EncodeList's streamed output
// to what encoding the whole List at once produces.
func TestEncodeListMatchesWholeListEncoding(t *testing.T) {
	objs := []map[string]any{
		{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{
			"name": "a", "annotations": map[string]any{"script": "line one\nline two\n  indented\n", "kubectl-fleet/context": "c1"},
		}, "spec": map[string]any{"containers": []any{map[string]any{"name": "x", "args": []any{"-v", "2"}}}, "tolerations": []any{}}},
		{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "b", "labels": map[string]any{}}, "status": map[string]any{"phase": "Running"}},
	}
	for _, n := range []int{0, 1, 2} {
		var items [][]byte
		whole := &unstructured.UnstructuredList{Object: map[string]any{
			"apiVersion": "v1", "kind": "List", "metadata": map[string]any{"resourceVersion": ""},
		}}
		for _, o := range objs[:n] {
			b, err := json.Marshal(o)
			if err != nil {
				t.Fatal(err)
			}
			items = append(items, b)
			whole.Items = append(whole.Items, unstructured.Unstructured{Object: o})
		}
		for _, f := range []Format{FormatJSON, FormatYAML} {
			t.Run(fmt.Sprintf("%s/%d items", f, n), func(t *testing.T) {
				var want, got bytes.Buffer
				if err := Encode(&want, whole, f); err != nil {
					t.Fatal(err)
				}
				if err := EncodeList(&got, items, f); err != nil {
					t.Fatal(err)
				}
				if got.String() != want.String() {
					t.Errorf("mismatch\n--- got\n%s\n--- want\n%s", got.String(), want.String())
				}
			})
		}
	}
}
