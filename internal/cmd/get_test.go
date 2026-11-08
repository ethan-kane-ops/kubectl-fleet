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

func TestGetCmdStrictFailsOnClusterError(t *testing.T) {
	c := newGetCmd(newFlags(tmpKubeconfigUnreachable(t)))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"pods", "--strict"})
	if err := c.Execute(); err == nil {
		t.Fatal("expected error, the only context is unreachable")
	}
}

func TestGetCmdWithoutStrictSucceeds(t *testing.T) {
	c := newGetCmd(newFlags(tmpKubeconfigUnreachable(t)))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"pods"})
	if err := c.Execute(); err != nil {
		t.Fatalf("cluster errors should only land in the table without --strict: %v", err)
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

func TestGetResolvesNamespacePerContext(t *testing.T) {
	a, b := &fakeCluster{}, &fakeCluster{}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "a", server: a.start(t), namespace: "team-a"},
		fakeContext{name: "b", server: b.start(t)},
	)
	cases := []struct {
		name  string
		args  []string
		wantA string
		wantB string
	}{
		{"context namespaces", []string{"pods"}, "/api/v1/namespaces/team-a/pods", "/api/v1/namespaces/default/pods"},
		{"--namespace overrides every context", []string{"pods", "-n", "ops"}, "/api/v1/namespaces/ops/pods", "/api/v1/namespaces/ops/pods"},
		{"-A lists cluster-wide", []string{"pods", "-A"}, "/api/v1/pods", "/api/v1/pods"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := NewRootCmd()
			c.SetOut(&bytes.Buffer{})
			c.SetErr(&bytes.Buffer{})
			c.SetArgs(append([]string{"get", "--kubeconfig", path, "--cache-dir", t.TempDir()}, tc.args...))
			if err := c.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			for _, chk := range []struct {
				cl   *fakeCluster
				want string
			}{{a, tc.wantA}, {b, tc.wantB}} {
				if !requested(chk.cl, chk.want) {
					t.Errorf("no request to %s; got %v", chk.want, chk.cl.paths())
				}
			}
		})
	}
}

// requested reports whether cl saw a request for path, ignoring the query.
func requested(cl *fakeCluster, path string) bool {
	for _, p := range cl.paths() {
		if strings.SplitN(p, "?", 2)[0] == path {
			return true
		}
	}
	return false
}

func TestGetNameOutputOmitsFailedContexts(t *testing.T) {
	ok := &fakeCluster{pods: []fakePod{{"default", "web"}}}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "down", server: "http://127.0.0.1:1"},
		fakeContext{name: "up", server: ok.start(t)},
	)
	c := newGetCmd(newFlags(path))
	var stdout, stderr bytes.Buffer
	c.SetOut(&stdout)
	c.SetErr(&stderr)
	c.SetArgs([]string{"pods", "-o", "name"})
	if err := c.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if got := stdout.String(); got != "up/default/web\n" {
		t.Errorf("stdout=%q want only the reachable pod", got)
	}
	if !strings.Contains(stderr.String(), "warn: down:") {
		t.Errorf("stderr should warn about the failed context: %q", stderr.String())
	}
}

func TestGetNamedObjectAcrossFleet(t *testing.T) {
	has := &fakeCluster{pods: []fakePod{{"default", "web"}}}
	lacks := &fakeCluster{}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "has", server: has.start(t)},
		fakeContext{name: "lacks", server: lacks.start(t)},
	)
	cases := []struct {
		name    string
		args    []string
		wantOut string
		wantErr string
	}{
		{"absent on some contexts is not a failure", []string{"pods", "web", "--strict", "-o", "name"}, "has/default/web\n", ""},
		{"absent everywhere is an error", []string{"pods", "nope"}, "", `pods "nope" not found in any reachable context`},
		{"name with selector is rejected", []string{"pods", "web", "-l", "app=web"}, "", "cannot be combined with --selector"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newGetCmd(newFlags(path))
			c.SilenceUsage = true
			var stdout bytes.Buffer
			c.SetOut(&stdout)
			c.SetErr(&bytes.Buffer{})
			c.SetArgs(tc.args)
			err := c.Execute()
			switch {
			case tc.wantErr == "" && err != nil:
				t.Fatalf("Execute: %v", err)
			case tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)):
				t.Fatalf("want error %q, got %v", tc.wantErr, err)
			}
			if stdout.String() != tc.wantOut {
				t.Errorf("stdout=%q want %q", stdout.String(), tc.wantOut)
			}
		})
	}
}
