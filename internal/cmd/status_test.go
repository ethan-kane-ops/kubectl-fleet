package cmd

import (
	"bytes"
	"encoding/json"
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
	c := newStatusCmd(newFlags(tmpKubeconfigUnreachable(t)))
	var stderr bytes.Buffer
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&stderr)
	c.SetArgs(nil)
	if err := c.Execute(); err != nil {
		t.Fatalf("cluster errors should only land in the table without --strict: %v", err)
	}
	if !strings.Contains(stderr.String(), "warn: prod:") {
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
