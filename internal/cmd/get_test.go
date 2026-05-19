package cmd

import (
	"bytes"
	"strings"
	"testing"
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
