package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/printers"
)

func TestGetCmdArgs(t *testing.T) {
	c := newGetCmd(newFlags(tmpKubeconfig(t)))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{})
	if err := c.Execute(); err == nil {
		t.Fatal("expected error when called with no args")
	}
}

func TestGetCmdStrictFailsOnClusterError(t *testing.T) {
	c := newGetCmd(newFlags(tmpKubeconfigUnreachable(t)))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"pods", "--strict"})
	if err := c.Execute(); err == nil {
		t.Fatal("expected error, the only context is unreachable")
	}
}

func TestGetCmdWithoutStrictSucceeds(t *testing.T) {
	c := newGetCmd(newFlags(tmpKubeconfigUnreachable(t)))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"pods"})
	if err := c.Execute(); err != nil {
		t.Fatalf("cluster errors should only land in the table without --strict: %v", err)
	}
}

func TestGetCmdNoMatchingContexts(t *testing.T) {
	c := newGetCmd(newFlags(tmpKubeconfig(t)))
	var buf bytes.Buffer
	c.SetOut(&buf)
	c.SetErr(&buf)
	c.SetArgs([]string{"pods", "--contexts", "does-not-match"})
	err := c.Execute()
	if err == nil || !strings.Contains(err.Error(), "no matching contexts") {
		t.Fatalf("expected no-matching-contexts error, got %v", err)
	}
}

func TestGetResolvesNamespacePerContext(t *testing.T) {
	a, b := &fakeCluster{}, &fakeCluster{}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "a", server: a.start(t), namespace: "team-a"},
		fakeContext{name: "b", server: b.start(t)},
	)
	cases := []struct {
		name  string
		args  []string
		wantA string
		wantB string
	}{
		{"context namespaces", []string{"pods"}, "/api/v1/namespaces/team-a/pods", "/api/v1/namespaces/default/pods"},
		{"--namespace overrides every context", []string{"pods", "-n", "ops"}, "/api/v1/namespaces/ops/pods", "/api/v1/namespaces/ops/pods"},
		{"-A lists cluster-wide", []string{"pods", "-A"}, "/api/v1/pods", "/api/v1/pods"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewRootCmd()
			c.SetOut(&bytes.Buffer{})
			c.SetErr(&bytes.Buffer{})
			c.SetArgs(append([]string{"get", "--kubeconfig", path, "--cache-dir", t.TempDir()}, tc.args...))
			if err := c.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			for _, chk := range []struct {
				cl   *fakeCluster
				want string
			}{{a, tc.wantA}, {b, tc.wantB}} {
				if !requested(chk.cl, chk.want) {
					t.Errorf("no request to %s; got %v", chk.want, chk.cl.paths())
				}
			}
		})
	}
}

// requested reports whether cl saw a request for path, ignoring the query.
func requested(cl *fakeCluster, path string) bool {
	for _, p := range cl.paths() {
		if strings.SplitN(p, "?", 2)[0] == path {
			return true
		}
	}
	return false
}

func TestGetNameOutputOmitsFailedContexts(t *testing.T) {
	ok := &fakeCluster{pods: []fakePod{{"default", "web"}}}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "down", server: "http://127.0.0.1:1"},
		fakeContext{name: "up", server: ok.start(t)},
	)
	c := newGetCmd(newFlags(path))
	var stdout, stderr bytes.Buffer
	c.SetOut(&stdout)
	c.SetErr(&stderr)
	c.SetArgs([]string{"pods", "-o", "name"})
	if err := c.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := stdout.String(); got != "up/default/web\n" {
		t.Errorf("stdout=%q want only the reachable pod", got)
	}
	if !strings.Contains(stderr.String(), "1 of 2 contexts failed") {
		t.Errorf("stderr should warn about the failed context: %q", stderr.String())
	}
}

func TestGetNamedObjectAcrossFleet(t *testing.T) {
	has := &fakeCluster{pods: []fakePod{{"default", "web"}}}
	lacks := &fakeCluster{pods: []fakePod{{"team-b", "web"}, {"team-b", "other"}}}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "has", server: has.start(t)},
		fakeContext{name: "lacks", server: lacks.start(t)},
	)
	cases := []struct {
		name    string
		args    []string
		wantOut string
		wantErr string
	}{
		{"absent on some contexts is not a failure", []string{"pods", "web", "--strict", "-o", "name"}, "has/default/web\n", ""},
		{"absent everywhere is an error", []string{"pods", "nope"}, "", `pods "nope" not found in any reachable context`},
		{"-A finds the name in any namespace", []string{"pods", "web", "-A", "-o", "name"}, "has/default/web\nlacks/team-b/web\n", ""},
		{"-A with the name nowhere is an error", []string{"pods", "nope", "-A"}, "", `pods "nope" not found in any reachable context`},
		{"name with selector is rejected", []string{"pods", "web", "-l", "app=web"}, "", "cannot be combined with --selector"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newGetCmd(newFlags(path))
			c.SilenceUsage = true
			var stdout bytes.Buffer
			c.SetOut(&stdout)
			c.SetErr(&bytes.Buffer{})
			c.SetArgs(tc.args)
			err := c.Execute()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("Execute: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("want error %q, got %v", tc.wantErr, err)
			}
			if stdout.String() != tc.wantOut {
				t.Errorf("stdout=%q want %q", stdout.String(), tc.wantOut)
			}
		})
	}
}

func TestListsArePaged(t *testing.T) {
	cl := &fakeCluster{}
	for i := range 600 {
		cl.pods = append(cl.pods, fakePod{"default", fmt.Sprintf("p%03d", i)})
	}
	path := writeFakeKubeconfig(t, fakeContext{name: "big", server: cl.start(t)})
	cases := []struct {
		name  string
		cmd   func() *cobra.Command
		args  []string
		check func(t *testing.T, out string)
	}{
		{"get", func() *cobra.Command { return newGetCmd(newFlags(path)) }, []string{"pods", "-o", "name"}, func(t *testing.T, out string) {
			if n := strings.Count(out, "\n"); n != 600 {
				t.Errorf("got %d pods want 600", n)
			}
		}},
		{"status", func() *cobra.Command { return newStatusCmd(newFlags(path)) }, []string{"-o", "json"}, func(t *testing.T, out string) {
			if !strings.Contains(out, `"podsTotal": 600`) {
				t.Errorf("want podsTotal 600 in:\n%s", out)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(cl.paths())
			c := tc.cmd()
			var out bytes.Buffer
			c.SetOut(&out)
			c.SetErr(&bytes.Buffer{})
			c.SetArgs(tc.args)
			if err := c.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			tc.check(t, out.String())
			pages := 0
			for _, p := range cl.paths()[before:] {
				if strings.Contains(p, "/pods?") && strings.Contains(p, "limit=500") {
					pages++
				}
			}
			if pages != 2 {
				t.Errorf("pod list requests with limit=500: %d want 2", pages)
			}
		})
	}
}

func TestGetStructuredOutputIsAListOfObjects(t *testing.T) {
	a := &fakeCluster{pods: []fakePod{{"default", "web"}}}
	b := &fakeCluster{pods: []fakePod{{"default", "web"}}}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "a", server: a.start(t)},
		fakeContext{name: "b", server: b.start(t)},
		fakeContext{name: "down", server: "http://127.0.0.1:1"},
	)
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			c := newGetCmd(newFlags(path))
			var stdout bytes.Buffer
			c.SetOut(&stdout)
			c.SetErr(&bytes.Buffer{})
			c.SetArgs([]string{"pods", "-o", format})
			if err := c.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			var list struct {
				Kind  string `json:"kind"`
				Items []struct {
					Kind     string `json:"kind"`
					Metadata struct {
						Name        string            `json:"name"`
						Annotations map[string]string `json:"annotations"`
					} `json:"metadata"`
				} `json:"items"`
			}
			if err := yaml.Unmarshal(stdout.Bytes(), &list); err != nil {
				t.Fatalf("decode: %v\n%s", err, stdout.String())
			}
			if list.Kind != "List" {
				t.Errorf("kind=%q want List", list.Kind)
			}
			var got []string
			for _, it := range list.Items {
				if it.Kind != "Pod" || it.Metadata.Name != "web" {
					t.Errorf("unexpected item %+v", it)
				}
				got = append(got, it.Metadata.Annotations[ContextAnnotation])
			}
			if strings.Join(got, ",") != "a,b" {
				t.Errorf("item contexts=%v want [a b], failed context left out", got)
			}
		})
	}
}

func TestBuiltinSchemaMatchesGroup(t *testing.T) {
	cases := []struct {
		name       string
		schema     func() printers.Schema
		wantSecond string
	}{
		{"core pods get pod columns", func() printers.Schema { return schemaFor(schema.GroupResource{Resource: "pods"}) }, "READY"},
		{"metrics pods fall back to generic", func() printers.Schema {
			return schemaFor(schema.GroupResource{Group: "metrics.k8s.io", Resource: "pods"})
		}, "AGE"},
		{"apps deployments get deployment columns", func() printers.Schema {
			return schemaFor(schema.GroupResource{Group: "apps", Resource: "deployments"})
		}, "READY"},
		{"user alias", func() printers.Schema { return fallbackSchema("po") }, "READY"},
		{"user input in another group", func() printers.Schema { return fallbackSchema("nodes.metrics.k8s.io") }, "AGE"},
		{"user input in the matching group", func() printers.Schema { return fallbackSchema("deployments.apps") }, "READY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if s := tc.schema(); s.Headers[1] != tc.wantSecond {
				t.Errorf("headers=%v want second column %s", s.Headers, tc.wantSecond)
			}
		})
	}
}

func TestGetReportsContextsResolvingAnotherGroup(t *testing.T) {
	a, b := &fakeCluster{widgetGroup: "a.example.io"}, &fakeCluster{widgetGroup: "b.example.io"}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "a", server: a.start(t)},
		fakeContext{name: "b", server: b.start(t)},
	)
	c := newGetCmd(newFlags(path))
	var stdout, stderr bytes.Buffer
	c.SetOut(&stdout)
	c.SetErr(&stderr)
	c.SetArgs([]string{"widgets", "--strict", "--errors"})
	err := c.Execute()
	if err == nil || !strings.Contains(err.Error(), "1 of 2 contexts failed") {
		t.Fatalf("want b counted as failed under --strict, got %v", err)
	}
	want := "warn: b: widgets resolves to widgets.b.example.io here, not widgets.a.example.io"
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr=%q want %q", stderr.String(), want)
	}
}

func TestBuildGetTableMergesColumnsByName(t *testing.T) {
	ok := func(ctx string, cols, wideCols []string, rows, wide [][]string) fleet.ClusterResult[fetchedResource] {
		return fleet.ClusterResult[fetchedResource]{Context: ctx, Value: fetchedResource{
			namespaced: true, columns: cols, wideColumns: wideCols, rows: rows, wide: wide,
		}}
	}
	results := []fleet.ClusterResult[fetchedResource]{
		// A newer cluster renders an extra OWNER column and only IP as wide.
		ok("a", []string{"NAME", "READY", "OWNER", "AGE"}, []string{"IP"},
			[][]string{{"ns1", "web", "1/1", "x", "1d"}}, [][]string{{"10.0.0.9"}}),
		// NODE is a default column here but wide elsewhere: it stays default.
		ok("b", []string{"NAME", "READY", "AGE", "NODE"}, []string{"IP"},
			[][]string{{"ns2", "api", "0/1", "2d", "n1"}}, [][]string{{"10.0.0.1"}}),
		ok("c", []string{"NAME", "READY", "AGE"}, []string{"IP", "NODE"},
			[][]string{{"ns3", "db", "1/1", "3d"}}, [][]string{{"10.0.0.2", "n2"}}),
		{Context: "d", Err: errors.New("down")},
	}
	tbl := buildGetTable("pods", results, output.FormatWide, false, true)
	if hidden := buildGetTable("pods", results, output.FormatWide, false, false); len(hidden.Rows) != 3 {
		t.Errorf("without --errors the failed context should have no row, got %d rows", len(hidden.Rows))
	}
	// --errors (showErrors) moves ERROR into the regular columns.
	wantHeaders := []string{"CONTEXT", "NAMESPACE", "NAME", "READY", "OWNER", "AGE", "NODE", "ERROR"}
	wantWide := []string{"IP"}
	if !reflect.DeepEqual(tbl.Headers, wantHeaders) || !reflect.DeepEqual(tbl.WideHeaders, wantWide) {
		t.Fatalf("headers=%v wide=%v\nwant %v %v", tbl.Headers, tbl.WideHeaders, wantHeaders, wantWide)
	}
	wantRows := [][]string{
		{"a", "ns1", "web", "1/1", "x", "1d", "", ""},
		{"b", "ns2", "api", "0/1", "", "2d", "n1", ""},
		{"c", "ns3", "db", "1/1", "", "3d", "n2", ""},
		{"d", "", "<error>", "", "", "", "", "down"},
	}
	wantWideRows := [][]string{{"10.0.0.9"}, {"10.0.0.1"}, {"10.0.0.2"}, {""}}
	if !reflect.DeepEqual(tbl.Rows, wantRows) {
		t.Errorf("rows=%v\nwant %v", tbl.Rows, wantRows)
	}
	if !reflect.DeepEqual(tbl.WideRows, wantWideRows) {
		t.Errorf("wide rows=%v\nwant %v", tbl.WideRows, wantWideRows)
	}
}

func TestColumnSetKeepsRelativeOrder(t *testing.T) {
	cases := []struct {
		name  string
		lists [][]string
		want  []string
	}{
		{"new column keeps its place", [][]string{{"NAME", "READY", "AGE"}, {"NAME", "READY", "OWNER", "AGE"}}, []string{"NAME", "READY", "OWNER", "AGE"}},
		{"new leading column", [][]string{{"NAME", "AGE"}, {"KIND", "NAME", "AGE"}}, []string{"KIND", "NAME", "AGE"}},
		{"new trailing columns", [][]string{{"NAME"}, {"NAME", "A", "B"}}, []string{"NAME", "A", "B"}},
		{"disjoint lists append", [][]string{{"A"}, {"B", "C"}}, []string{"A", "B", "C"}},
		{"unknown run before a known column", [][]string{{"NAME", "AGE"}, {"X", "Y", "AGE"}}, []string{"NAME", "X", "Y", "AGE"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var c columnSet
			for _, l := range tc.lists {
				c.add(l...)
			}
			if !reflect.DeepEqual(c.names, tc.want) {
				t.Errorf("got %v want %v", c.names, tc.want)
			}
			for i, n := range c.names {
				if c.index[n] != i {
					t.Errorf("index[%s]=%d want %d", n, c.index[n], i)
				}
			}
		})
	}
}

func TestGetServerSideTables(t *testing.T) {
	tables := &fakeCluster{pods: []fakePod{{"default", "web"}}, extraColumn: "Owner"}
	plain := &fakeCluster{pods: []fakePod{{"default", "api"}}, noTables: true}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "plain", server: plain.start(t)},
		fakeContext{name: "tables", server: tables.start(t)},
	)
	cases := []struct {
		name       string
		args       []string
		wantTable  bool
		wantOut    []string
		notWantOut []string
	}{
		{
			name:      "server columns, with a fallback context",
			args:      []string{"pods"},
			wantTable: true,
			wantOut:   []string{`RESTARTS\s+OWNER\s+AGE`, `tables\s+default\s+web\s+1/1\s+Running\s+3\s+x\s+1d`, `plain\s+default\s+api\s+\S+\s+Running`},
		},
		{
			name:       "--server-print=false uses built-in columns",
			args:       []string{"pods", "--server-print=false"},
			notWantOut: []string{"OWNER"},
			wantOut:    []string{"web", "api"},
		},
		{
			name:      "named lookup through the table",
			args:      []string{"pods", "web", "-o", "name"},
			wantTable: true,
			wantOut:   []string{"tables/default/web\n"},
		},
		{
			name:       "json never asks for a table",
			args:       []string{"pods", "-o", "json"},
			wantOut:    []string{`"kind": "Pod"`},
			notWantOut: []string{"OWNER"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(tables.paths())
			c := newGetCmd(newFlags(path))
			var stdout bytes.Buffer
			c.SetOut(&stdout)
			c.SetErr(&bytes.Buffer{})
			c.SetArgs(tc.args)
			if err := c.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			out := stdout.String()
			for _, w := range tc.wantOut {
				if !regexp.MustCompile(w).MatchString(out) {
					t.Errorf("missing %q in:\n%s", w, out)
				}
			}
			for _, w := range tc.notWantOut {
				if strings.Contains(out, w) {
					t.Errorf("unexpected %q in:\n%s", w, out)
				}
			}
			asked := strings.Contains(tables.acceptFor("/api/v1/namespaces/default/pods"), "as=Table") ||
				strings.Contains(tables.acceptFor("/api/v1/namespaces/default/pods/web"), "as=Table")
			metadataOnly := false
			for _, p := range tables.paths()[before:] {
				if strings.Contains(p, "/pods") && strings.Contains(p, "includeObject=Metadata") {
					metadataOnly = true
				}
			}
			if tc.wantTable && (!asked || !metadataOnly) {
				t.Errorf("table request: Accept as=Table %v, includeObject=Metadata %v", asked, metadataOnly)
			}
			if !tc.wantTable && metadataOnly {
				t.Errorf("did not expect a table request: %v", tables.paths()[before:])
			}
		})
	}
}
