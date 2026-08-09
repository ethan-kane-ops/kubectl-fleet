package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/cli-runtime/pkg/genericclioptions"
)

const fixtureKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: c-prod
  cluster: {server: https://prod.example.test:6443}
- name: c-stage
  cluster: {server: https://stage.example.test:6443}
users:
- name: u
  user: {token: t}
contexts:
- name: prod
  context: {cluster: c-prod, user: u, namespace: kube-system}
- name: stage
  context: {cluster: c-stage, user: u}
current-context: prod
`

func tmpKubeconfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(path, []byte(fixtureKubeconfig), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func newFlags(path string) *genericclioptions.ConfigFlags {
	f := genericclioptions.NewConfigFlags(true)
	f.KubeConfig = &path
	return f
}

// fixtureKubeconfigUnreachable points at a loopback port nothing listens on,
// so every call fails fast with "connection refused" (no DNS lookup, no
// hang) — deterministic per-context failures for exercising --strict.
const fixtureKubeconfigUnreachable = `apiVersion: v1
kind: Config
clusters:
- name: c-prod
  cluster: {server: https://127.0.0.1:1}
users:
- name: u
  user: {token: t}
contexts:
- name: prod
  context: {cluster: c-prod, user: u}
current-context: prod
`

func tmpKubeconfigUnreachable(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(path, []byte(fixtureKubeconfigUnreachable), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestContextsList(t *testing.T) {
	c := newContextsCmd(newFlags(tmpKubeconfig(t)))
	var buf bytes.Buffer
	c.SetOut(&buf)
	c.SetErr(&buf)
	c.SetArgs(nil)
	if err := c.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"CONTEXT", "prod", "stage", "kube-system"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "REACHABLE") {
		t.Errorf("REACHABLE column should not appear without --check:\n%s", out)
	}
}

func TestContextsFilter(t *testing.T) {
	c := newContextsCmd(newFlags(tmpKubeconfig(t)))
	var buf bytes.Buffer
	c.SetOut(&buf)
	c.SetErr(&buf)
	c.SetArgs([]string{"--filter", "^prod$"})
	if err := c.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "prod") {
		t.Errorf("missing prod:\n%s", out)
	}
	if strings.Contains(out, "stage") {
		t.Errorf("stage should be filtered out:\n%s", out)
	}
}

func TestContextsStrictWithoutCheckIsNoop(t *testing.T) {
	c := newContextsCmd(newFlags(tmpKubeconfigUnreachable(t)))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"--strict"})
	if err := c.Execute(); err != nil {
		t.Fatalf("--strict without --check should not probe: %v", err)
	}
}

func TestContextsStrictWithCheckFailsOnUnreachable(t *testing.T) {
	c := newContextsCmd(newFlags(tmpKubeconfigUnreachable(t)))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"--check", "--strict", "--timeout", "2s"})
	if err := c.Execute(); err == nil {
		t.Fatal("expected error, all contexts are unreachable")
	}
}

func TestContextsCheckWithoutStrictSucceeds(t *testing.T) {
	c := newContextsCmd(newFlags(tmpKubeconfigUnreachable(t)))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"--check", "--timeout", "2s"})
	if err := c.Execute(); err != nil {
		t.Fatalf("failures should only be reported in the table without --strict: %v", err)
	}
}

func TestContextsJSON(t *testing.T) {
	c := newContextsCmd(newFlags(tmpKubeconfig(t)))
	var buf bytes.Buffer
	c.SetOut(&buf)
	c.SetErr(&buf)
	c.SetArgs([]string{"-o", "json"})
	if err := c.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `"CONTEXT": "prod"`) {
		t.Errorf("json missing CONTEXT=prod:\n%s", out)
	}
}
