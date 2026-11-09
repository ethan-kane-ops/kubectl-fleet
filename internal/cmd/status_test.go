package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/health"
)

func TestStatusNoMatchingContexts(t *testing.T) {
	c := newStatusCmd(newFlags(tmpKubeconfig(t)))
	var buf bytes.Buffer
	c.SetOut(&buf)
	c.SetErr(&buf)
	c.SetArgs([]string{"--contexts", "does-not-match"})
	err := c.Execute()
	if err == nil || !strings.Contains(err.Error(), "no matching contexts") {
		t.Fatalf("expected no-matching-contexts, got %v", err)
	}
}

func TestStatusStrictFailsOnClusterError(t *testing.T) {
	c := newStatusCmd(newFlags(tmpKubeconfigUnreachable(t)))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"--strict"})
	if err := c.Execute(); err == nil {
		t.Fatal("expected error, the only context is unreachable")
	}
}

func TestStatusWithoutStrictSucceeds(t *testing.T) {
	c := newStatusCmd(newFlags(partialFleet(t)))
	var stderr bytes.Buffer
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&stderr)
	c.SetArgs(nil)
	if err := c.Execute(); err != nil {
		t.Fatalf("one failed context of two should not fail the command without --strict: %v", err)
	}
	if !strings.Contains(stderr.String(), "1 of 2 contexts failed") {
		t.Errorf("stderr should warn about the failed context: %q", stderr.String())
	}
}

func TestFormatNoisy(t *testing.T) {
	in := []health.NamespaceNoise{
		{Namespace: "kube-system", Unhealthy: 3},
		{Namespace: "default", Unhealthy: 1},
	}
	got := formatNoisy(in)
	want := "kube-system(3),default(1)"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if formatNoisy(nil) != "" {
		t.Error("nil should be empty")
	}
}

func TestStatusStructuredOutput(t *testing.T) {
	up := &fakeCluster{pods: []fakePod{{"default", "a"}, {"default", "b"}}}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "down", server: "http://127.0.0.1:1"},
		fakeContext{name: "up", server: up.start(t)},
	)
	c := newStatusCmd(newFlags(path))
	var stdout bytes.Buffer
	c.SetOut(&stdout)
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"-o", "json"})
	if err := c.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	var records []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &records); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	if len(records) != 2 {
		t.Fatalf("records=%d want 2:\n%s", len(records), stdout.String())
	}
	down, upRec := records[0], records[1]
	if down["context"] != "down" || down["error"] == nil || down["podsTotal"] != nil {
		t.Errorf("failed context should carry only context and error: %v", down)
	}
	if upRec["context"] != "up" || upRec["podsTotal"] != float64(2) || upRec["serverVersion"] != "v1.31.0" {
		t.Errorf("healthy context record: %v", upRec)
	}
	if _, ok := upRec["error"]; ok {
		t.Errorf("healthy context should have no error key: %v", upRec)
	}
}

func TestShortDuration(t *testing.T) {
	cases := map[time.Duration]string{
		time.Hour:                    "1h",
		90 * time.Minute:             "1h30m",
		5 * time.Minute:              "5m",
		90 * time.Second:             "1m30s",
		30 * time.Second:             "30s",
		time.Hour + 5*time.Second:    "1h0m5s",
		25*time.Hour + 2*time.Minute: "25h2m",
	}
	for d, want := range cases {
		if got := shortDuration(d); got != want {
			t.Errorf("shortDuration(%v)=%q want %q", d, got, want)
		}
	}
}

func TestStatusRequestsProtobufAndPagesNodes(t *testing.T) {
	cl := &fakeCluster{pods: []fakePod{{"default", "a"}}}
	c := newStatusCmd(newFlags(writeFakeKubeconfig(t, fakeContext{name: "c", server: cl.start(t)})))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	if err := c.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, path := range []string{"/api/v1/pods", "/api/v1/nodes"} {
		if got := cl.acceptFor(path); !strings.HasPrefix(got, "application/vnd.kubernetes.protobuf") {
			t.Errorf("%s Accept=%q want protobuf first", path, got)
		}
	}
	if !requested(cl, "/api/v1/nodes") || !strings.Contains(strings.Join(cl.paths(), " "), "/api/v1/nodes?limit=500") {
		t.Errorf("node list not paged: %v", cl.paths())
	}
}

func TestStatusProblemsSortTop(t *testing.T) {
	pods := func(n int) []fakePod {
		var out []fakePod
		for i := range n {
			out = append(out, fakePod{"default", fmt.Sprintf("p%d", i)})
		}
		return out
	}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "big", server: (&fakeCluster{nodes: 5, pods: pods(3), gitVersion: "v1.30.2"}).start(t)},
		fakeContext{name: "sick", server: (&fakeCluster{nodes: 3, notReadyNodes: 1, pods: pods(2), gitVersion: "v1.31.1"}).start(t)},
		fakeContext{name: "small", server: (&fakeCluster{nodes: 2, pods: pods(1), gitVersion: "v1.29.9"}).start(t)},
	)
	cases := []struct {
		name    string
		args    []string
		want    []string // contexts in output order
		wantErr string
	}{
		{"default order", nil, []string{"big", "sick", "small"}, ""},
		{"problems only", []string{"--problems"}, []string{"sick"}, ""},
		{"largest by pods, top 2", []string{"--sort-by", "pods", "--top", "2"}, []string{"big", "sick"}, ""},
		{"largest by nodes", []string{"--sort-by", "nodes"}, []string{"big", "sick", "small"}, ""},
		{"newest version first", []string{"--sort-by", "version"}, []string{"sick", "big", "small"}, ""},
		{"context ascending", []string{"--sort-by", "context", "--top", "1"}, []string{"big"}, ""},
		{"unknown sort key", []string{"--sort-by", "colour"}, nil, "--sort-by must be one of: nodes, pods, version, context"},
		{"negative top", []string{"--top", "-1"}, nil, "--top must be 0 or more"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := runRoot(t, append(append([]string{"status", "--no-headers"}, tc.args...), "--kubeconfig", path, "--cache-dir", t.TempDir())...)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
				if f := strings.Fields(line); len(f) > 0 {
					got = append(got, f[0])
				}
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("contexts %v want %v\n%s", got, tc.want, out)
			}
		})
	}
}

func TestStatusTopAppliesToJSON(t *testing.T) {
	path := writeFakeKubeconfig(t,
		fakeContext{name: "a", server: (&fakeCluster{nodes: 1}).start(t)},
		fakeContext{name: "b", server: (&fakeCluster{nodes: 4}).start(t)},
	)
	out, _, err := runRoot(t, "status", "--sort-by", "nodes", "--top", "1", "-o", "json", "--kubeconfig", path, "--cache-dir", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	if err := json.Unmarshal([]byte(out), &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0]["context"] != "b" {
		t.Errorf("want only b, got %v", records)
	}
}
