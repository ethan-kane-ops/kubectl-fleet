package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestContextsFilterAliasStillWorks(t *testing.T) {
	c := NewRootCmd()
	var stdout, stderr bytes.Buffer
	c.SetOut(&stdout)
	c.SetErr(&stderr)
	c.SetArgs([]string{"contexts", "--filter", "^prod$", "--kubeconfig", tmpKubeconfig(t)})
	if err := c.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(stdout.String(), "prod") || strings.Contains(stdout.String(), "stage") {
		t.Errorf("--filter should still select only prod:\n%s", stdout.String())
	}
	// cobra prints flag deprecations with Print: to the Out writer when one
	// is set (as here), to stderr when not (the real binary).
	if !strings.Contains(stdout.String()+stderr.String(), "use --contexts") {
		t.Errorf("want a deprecation notice pointing at --contexts, got stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestEveryFleetCommandSharesSelectionFlags(t *testing.T) {
	root := NewRootCmd()
	for _, name := range []string{"contexts", "get", "status", "version"} {
		c, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatalf("find %s: %v", name, err)
		}
		for _, flag := range []string{"contexts", "exclude", "parallelism", "strict", "output", "no-headers"} {
			if c.Flags().Lookup(flag) == nil {
				t.Errorf("%s is missing --%s", name, flag)
			}
		}
	}
}

func TestNoContextsLeftAfterExclude(t *testing.T) {
	for _, args := range [][]string{{"get", "pods"}, {"status"}, {"version"}} {
		c := NewRootCmd()
		c.SetOut(&bytes.Buffer{})
		c.SetErr(&bytes.Buffer{})
		c.SetArgs(append(args, "--exclude", ".", "--kubeconfig", tmpKubeconfig(t)))
		err := c.Execute()
		if err == nil || !strings.Contains(err.Error(), "no matching contexts") {
			t.Errorf("%v: want 'no matching contexts', got %v", args, err)
		}
	}
}
