package kubeconfig

import (
	"os"
	"path/filepath"
	"testing"

	"k8s.io/cli-runtime/pkg/genericclioptions"
)

const sampleKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: c-prod
  cluster: {server: https://prod.example.test:6443}
- name: c-stage
  cluster: {server: https://stage.example.test:6443}
- name: c-dev
  cluster: {server: https://dev.example.test:6443}
users:
- name: u
  user: {token: t}
contexts:
- name: prod
  context: {cluster: c-prod, user: u, namespace: kube-system}
- name: stage
  context: {cluster: c-stage, user: u}
- name: dev
  context: {cluster: c-dev, user: u, namespace: default}
current-context: prod
`

func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "kubeconfig")
	if err := os.WriteFile(path, []byte(sampleKubeconfig), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func flagsWith(path string) *genericclioptions.ConfigFlags {
	f := genericclioptions.NewConfigFlags(true)
	f.KubeConfig = &path
	return f
}

func TestLoad(t *testing.T) {
	cfg, err := Load(flagsWith(writeFixture(t)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := len(cfg.Contexts), 3; got != want {
		t.Fatalf("contexts len=%d want %d", got, want)
	}
}

func TestContextsAllSorted(t *testing.T) {
	cfg, err := Load(flagsWith(writeFixture(t)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	refs, err := Contexts(cfg, "")
	if err != nil {
		t.Fatalf("Contexts: %v", err)
	}
	want := []string{"dev", "prod", "stage"}
	if len(refs) != len(want) {
		t.Fatalf("len=%d want %d", len(refs), len(want))
	}
	for i, name := range want {
		if refs[i].Name != name {
			t.Errorf("refs[%d].Name=%q want %q", i, refs[i].Name, name)
		}
	}
	for _, r := range refs {
		if r.Name == "prod" && r.Namespace != "kube-system" {
			t.Errorf("prod ns=%q want kube-system", r.Namespace)
		}
	}
}

func TestContextsRegexFilter(t *testing.T) {
	cfg, err := Load(flagsWith(writeFixture(t)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	refs, err := Contexts(cfg, "^(prod|stage)$")
	if err != nil {
		t.Fatalf("Contexts: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("len=%d want 2", len(refs))
	}
	if refs[0].Name != "prod" || refs[1].Name != "stage" {
		t.Errorf("unexpected ordering: %v", refs)
	}
}

func TestContextsBadRegex(t *testing.T) {
	cfg, err := Load(flagsWith(writeFixture(t)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, err := Contexts(cfg, "("); err == nil {
		t.Fatal("expected error on bad regex")
	}
}

func TestRESTConfigFor(t *testing.T) {
	path := writeFixture(t)
	rc, err := RESTConfigFor(flagsWith(path), "stage")
	if err != nil {
		t.Fatalf("RESTConfigFor: %v", err)
	}
	if rc.Host != "https://stage.example.test:6443" {
		t.Errorf("host=%q", rc.Host)
	}
}

func TestRESTConfigForMissingContext(t *testing.T) {
	path := writeFixture(t)
	if _, err := RESTConfigFor(flagsWith(path), "nope"); err == nil {
		t.Fatal("expected error on missing context")
	}
}
