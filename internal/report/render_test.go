package report

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/health"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/inventory"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
)

var update = flag.Bool("update", false, "rewrite golden files")

func fixtureReport() Report {
	ok := healthy("v1.30.4-eks-a737599")
	ok.Components = []inventory.Component{{Name: "coredns", Kind: "Deployment", Version: "v1.11.3"}}
	sick := healthy("v1.30.4-eks-a737599")
	sick.NodesTotal, sick.NodesReady = 4, 4
	sick.Kubelets = []inventory.Count{{Value: "v1.30.4-eks-a737599", N: 3}, {Value: "v1.29.8-eks-a737599", N: 1}}
	sick.Zones = []inventory.Count{{Value: "eu-west-1a", N: 2}, {Value: "eu-west-1b", N: 2}}
	sick.InstanceTypes = []inventory.Count{{Value: "m7g.large", N: 3}, {Value: "unknown", N: 1}}
	sick.Provisioners = []Provisioner{{Kind: "karpenter", Name: "general", Nodes: 3}, {Kind: "nodegroup", Name: "system", Nodes: 1}}
	sick.PodsCrashLoop, sick.CrashLoopNS = 2, []health.NamespaceNoise{{Namespace: "payments", Unhealthy: 2}}
	sick.Components = []inventory.Component{{Name: "coredns", Kind: "Deployment", Version: "v1.11.1"}}
	return Build(t0, Selection{Contexts: []string{"^prod-", "arn:aws:eks"}, ExpectVersion: "1.30", PendingFor: "5m"}, []Result{
		{Context: "prod-eu-1", Cluster: ok},
		{Context: "arn:aws:eks:eu-west-1:1:cluster/a|b<c>", Cluster: sick},
		{Context: "prod-us-1", Cause: "connection refused"},
	})
}

func TestRenderGolden(t *testing.T) {
	for f, file := range map[output.Format]string{FormatText: "report.txt", output.FormatMarkdown: "report.md", output.FormatHTML: "report.html"} {
		t.Run(string(f), func(t *testing.T) {
			var buf bytes.Buffer
			if err := Render(&buf, fixtureReport(), f); err != nil {
				t.Fatalf("Render: %v", err)
			}
			path := filepath.Join("testdata", file)
			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read golden (run with -update): %v", err)
			}
			if buf.String() != string(want) {
				t.Errorf("%s differs from golden; got:\n%s", file, buf.String())
			}
		})
	}
}

func TestRenderJSONShape(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, fixtureReport(), output.FormatJSON); err != nil {
		t.Fatalf("Render: %v", err)
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, k := range []string{"generatedAt", "selection", "summary", "attention", "clusters", "components"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("json lacks %q", k)
		}
	}
}

func TestRenderHTMLIsSelfContained(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, fixtureReport(), output.FormatHTML); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "<script") || strings.Contains(out, "http") {
		t.Errorf("html must carry no scripts or links")
	}
	if !strings.Contains(out, "a|b&lt;c&gt;") {
		t.Errorf("context name not escaped")
	}
}

func TestRenderRejectsTable(t *testing.T) {
	for _, f := range []output.Format{output.FormatTable, output.FormatWide, output.FormatName} {
		if err := Render(&bytes.Buffer{}, fixtureReport(), f); err == nil || !strings.Contains(err.Error(), "text|markdown|html|json|yaml") {
			t.Errorf("%s: want an error listing valid formats, got %v", f, err)
		}
	}
}

func TestViewRowsForEmptyAndForbidden(t *testing.T) {
	empty := &Cluster{ControlPlane: "v1.30.4"}
	noNodes := healthy("v1.30.4")
	noNodes.NodesForbidden = true
	noNodes.NodesReady, noNodes.NodesTotal, noNodes.Kubelets, noNodes.Zones = 0, 0, nil, nil
	noPods := healthy("v1.30.4")
	noPods.PodsForbidden = true
	noPods.PodsRunning = 0
	v := newView(Build(t0, Selection{PendingFor: "5m"}, []Result{
		{Context: "a-empty", Cluster: empty}, {Context: "b-nonodes", Cluster: noNodes}, {Context: "c-nopods", Cluster: noPods},
	}))
	// CONTEXT VERSION KUBELETS NODES ZONES PODS PENDING CRASHLOOP PROVISIONERS
	want := [][]string{
		{"a-empty", "v1.30.4", "-", "0/0", "-", "0", "0", "0", "-"},
		{"b-nonodes", "v1.30.4", "-", "-", "-", "10", "0", "0", "-"},
		{"c-nopods", "v1.30.4", "1.30.4", "3/3", "1", "-", "-", "-", "karpenter 3"},
	}
	for i, w := range want {
		if strings.Join(v.ClusterRows[i], ",") != strings.Join(w, ",") {
			t.Errorf("row %d = %v, want %v", i, v.ClusterRows[i], w)
		}
	}
}
