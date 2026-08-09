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
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs(nil)
	if err := c.Execute(); err != nil {
		t.Fatalf("cluster errors should only land in the table without --strict: %v", err)
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
