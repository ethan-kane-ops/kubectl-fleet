package kubeconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
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

func TestNamespace(t *testing.T) {
	path := writeFixture(t)
	cases := []struct {
		name   string
		ctx    string
		flagNS string
		want   string
	}{
		{"context namespace", "prod", "", "kube-system"},
		{"no context namespace falls back to default", "stage", "", "default"},
		{"flag overrides context", "prod", "team-a", "team-a"},
		{"flag applies to context without namespace", "stage", "team-a", "team-a"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := flagsWith(path)
			f.Namespace = &tc.flagNS
			got, err := Namespace(ForContext(f, tc.ctx))
			if err != nil {
				t.Fatalf("Namespace: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestRESTConfigForHonorsGlobalFlags(t *testing.T) {
	path := writeFixture(t)
	f := flagsWith(path)
	as, token, timeout := "reader", "override-token", "7s"
	groups := []string{"g1"}
	f.Impersonate, f.BearerToken, f.Timeout, f.ImpersonateGroup = &as, &token, &timeout, &groups
	rc, err := RESTConfigFor(f, "stage")
	if err != nil {
		t.Fatalf("RESTConfigFor: %v", err)
	}
	if rc.Impersonate.UserName != as || len(rc.Impersonate.Groups) != 1 || rc.Impersonate.Groups[0] != "g1" {
		t.Errorf("impersonate=%+v", rc.Impersonate)
	}
	if rc.BearerToken != token {
		t.Errorf("token=%q", rc.BearerToken)
	}
	if rc.Timeout.String() != "7s" {
		t.Errorf("timeout=%v", rc.Timeout)
	}
	if rc.Host != "https://stage.example.test:6443" {
		t.Errorf("host=%q", rc.Host)
	}
}

func TestValidateFleetFlags(t *testing.T) {
	set := func(apply func(f *genericclioptions.ConfigFlags)) *genericclioptions.ConfigFlags {
		f := genericclioptions.NewConfigFlags(true)
		apply(f)
		return f
	}
	val := "x"
	cases := []struct {
		name    string
		flags   *genericclioptions.ConfigFlags
		wantErr string
	}{
		{"defaults pass", genericclioptions.NewConfigFlags(true), ""},
		{"impersonation passes", set(func(f *genericclioptions.ConfigFlags) { f.Impersonate = &val }), ""},
		{"token without --context rejected", set(func(f *genericclioptions.ConfigFlags) { f.BearerToken = &val }), "--token requires --context"},
		{"password without --context rejected", set(func(f *genericclioptions.ConfigFlags) { f.Password = &val }), "--password requires --context"},
		{"token with --context passes", set(func(f *genericclioptions.ConfigFlags) { f.BearerToken = &val; f.Context = &val }), ""},
		{"context passes", set(func(f *genericclioptions.ConfigFlags) { f.Context = &val }), ""},
		{"cluster rejected", set(func(f *genericclioptions.ConfigFlags) { f.ClusterName = &val }), "--cluster"},
		{"server rejected", set(func(f *genericclioptions.ConfigFlags) { f.APIServer = &val }), "--server"},
		{"tls server name rejected", set(func(f *genericclioptions.ConfigFlags) { f.TLSServerName = &val }), "--tls-server-name"},
		{"user rejected", set(func(f *genericclioptions.ConfigFlags) { f.AuthInfoName = &val }), "--user"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFleetFlags(tc.flags)
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestSelect(t *testing.T) {
	cfg, err := Load(flagsWith(writeFixture(t)))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cases := []struct {
		name    string
		context string
		filter  string
		want    []string
		wantErr bool
	}{
		{"no --context uses the filter", "", "^(prod|dev)$", []string{"dev", "prod"}, false},
		{"--context overrides the filter", "stage", "^prod$", []string{"stage"}, false},
		{"unknown --context errors", "nope", "", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := genericclioptions.NewConfigFlags(true)
			f.Context = &tc.context
			refs, err := Select(f, cfg, tc.filter)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			var got []string
			for _, r := range refs {
				got = append(got, r.Name)
			}
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestServerWarningsPrintedOncePerContext(t *testing.T) {
	path := writeFixture(t)
	var buf strings.Builder
	orig := serverWarnings
	serverWarnings = &warningPrinter{out: &buf, seen: map[string]bool{}}
	t.Cleanup(func() { serverWarnings = orig })

	wrapped := 0
	base := flagsWith(path)
	base.WrapConfigFn = func(c *rest.Config) *rest.Config { wrapped++; return c }
	for _, ctx := range []string{"prod", "stage"} {
		// Two clients per context, as typed and discovery clients would be.
		for range 2 {
			rc, err := RESTConfigFor(base, ctx)
			if err != nil {
				t.Fatalf("RESTConfigFor: %v", err)
			}
			rc.WarningHandler.HandleWarningHeader(299, "", "v1beta1 is deprecated")
			rc.WarningHandler.HandleWarningHeader(199, "", "not a deprecation warning")
		}
	}
	want := "warn: prod: v1beta1 is deprecated\nwarn: stage: v1beta1 is deprecated\n"
	if buf.String() != want {
		t.Errorf("got %q want %q", buf.String(), want)
	}
	if wrapped != 4 {
		t.Errorf("user WrapConfigFn ran %d times, want 4", wrapped)
	}
}

func TestForContextRateLimits(t *testing.T) {
	rc, err := RESTConfigFor(flagsWith(writeFixture(t)), "prod")
	if err != nil {
		t.Fatalf("RESTConfigFor: %v", err)
	}
	if rc.QPS != requestQPS || rc.Burst != requestBurst {
		t.Errorf("qps=%v burst=%d want %v/%d", rc.QPS, rc.Burst, requestQPS, requestBurst)
	}
}
