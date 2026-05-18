package cmd

import (
	"bytes"
	"strings"
	"testing"

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

func TestFormatNoisy(t *testing.T) {
	in := []health.NamespaceNoise{
		{Namespace: "kube-system", NonRunning: 3},
		{Namespace: "default", NonRunning: 1},
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
