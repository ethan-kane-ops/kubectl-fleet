package cmd

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/buildinfo"
)

func TestVersionCmd_clientOnly(t *testing.T) {
	c := newVersionCmd(newFlags(tmpKubeconfig(t)))
	var out, errBuf bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errBuf)
	c.SetArgs([]string{"--client"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := out.String()
	if !strings.Contains(body, "kubectl-fleet") {
		t.Fatalf("missing kubectl-fleet component, got:\n%s", body)
	}
	if !strings.Contains(body, buildinfo.Version) {
		t.Fatalf("missing client version %q, got:\n%s", buildinfo.Version, body)
	}
}

func TestVersionCmd_clientOnlyJSON(t *testing.T) {
	c := newVersionCmd(newFlags(tmpKubeconfig(t)))
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"--client", "-o", "json"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rows []map[string]string
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (client only), got %d", len(rows))
	}
	if rows[0]["COMPONENT"] != "kubectl-fleet" {
		t.Errorf("COMPONENT = %q", rows[0]["COMPONENT"])
	}
	if rows[0]["VERSION"] != buildinfo.Version {
		t.Errorf("VERSION = %q want %q", rows[0]["VERSION"], buildinfo.Version)
	}
}

func TestVersionCmd_strictFailsOnClusterError(t *testing.T) {
	c := newVersionCmd(newFlags(tmpKubeconfigUnreachable(t)))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"--strict"})
	if err := c.Execute(); err == nil {
		t.Fatal("expected error, the only context is unreachable")
	}
}

func TestVersionCmd_withoutStrictSucceeds(t *testing.T) {
	c := newVersionCmd(newFlags(partialFleet(t)))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs(nil)
	if err := c.Execute(); err != nil {
		t.Fatalf("one failed context of two should not fail the command without --strict: %v", err)
	}
}

func TestVersionCmd_noMatchingContexts(t *testing.T) {
	c := newVersionCmd(newFlags(tmpKubeconfig(t)))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"--contexts", "does-not-match"})
	err := c.Execute()
	if err == nil || !strings.Contains(err.Error(), "no matching contexts") {
		t.Fatalf("expected no-matching-contexts error, got %v", err)
	}
}

func TestClientRow(t *testing.T) {
	cols, wide := clientRow()
	if cols[0] != "(client)" {
		t.Errorf("CONTEXT = %q", cols[0])
	}
	if cols[1] != "kubectl-fleet" {
		t.Errorf("COMPONENT = %q", cols[1])
	}
	if cols[2] != buildinfo.Version {
		t.Errorf("VERSION = %q want %q", cols[2], buildinfo.Version)
	}
	if wide[0] != buildinfo.Commit {
		t.Errorf("GIT_COMMIT = %q", wide[0])
	}
	if wide[1] != buildinfo.Date {
		t.Errorf("BUILD_DATE = %q", wide[1])
	}
}

func TestVersionDetailed(t *testing.T) {
	sys := map[string]string{"coredns": "registry.k8s.io/coredns/coredns:v1.11.1"}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "a", server: (&fakeCluster{nodes: 3, kubelet: "v1.30.4", gitVersion: "v1.30.4", systemDeployments: sys,
			systemDaemonSets: map[string]string{"kube-proxy": "registry.k8s.io/kube-proxy:v1.30.4"}}).start(t)},
		fakeContext{name: "b", server: (&fakeCluster{nodes: 2, kubelet: "v1.30.4", gitVersion: "v1.30.4", systemDeployments: sys}).start(t)},
		fakeContext{name: "dead", server: "http://127.0.0.1:1"},
	)
	cases := []struct {
		name       string
		args       []string
		want       []string
		notWant    []string
		wantStderr string
	}{
		{
			name: "fleet spread by default",
			args: []string{"version", "--detailed"},
			want: []string{`(?m)^COMPONENT\s+VERSION\s+CLUSTERS$`, `kube-apiserver\s+v1\.30\.4\s+2`, `kubelet\s+v1\.30\.4\s+2`,
				`coredns\s+v1\.11\.1\s+2`, `kube-proxy\s+v1\.30\.4\s+1`, `container-runtime\s+containerd://2\.1\.1\s+2`},
			notWant:    []string{`kubectl-fleet`},
			wantStderr: "1 of 3 contexts failed",
		},
		{
			name: "one row per cluster",
			args: []string{"version", "--detailed", "--per-cluster"},
			want: []string{`(?m)^CONTEXT\s+COMPONENT\s+VERSION\s+NODES$`, `a\s+kubelet\s+v1\.30\.4\s+3`, `b\s+kubelet\s+v1\.30\.4\s+2`, `a\s+kube-proxy\s+v1\.30\.4`},
		},
		{
			name: "json carries per-cluster detail",
			args: []string{"version", "--detailed", "-o", "json"},
			want: []string{`"context": "a"`, `"kubelets": \[`, `"components": \[`, `"context": "dead",\s+"error"`},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, err := runRoot(t, append(tc.args, "--kubeconfig", path, "--cache-dir", t.TempDir())...)
			if err != nil {
				t.Fatalf("Execute: %v", err)
			}
			for _, w := range tc.want {
				if !regexp.MustCompile(w).MatchString(out) {
					t.Errorf("output does not match %q:\n%s", w, out)
				}
			}
			for _, w := range tc.notWant {
				if regexp.MustCompile(w).MatchString(out) {
					t.Errorf("output should not match %q:\n%s", w, out)
				}
			}
			if tc.wantStderr != "" && !strings.Contains(errOut, tc.wantStderr) {
				t.Errorf("stderr %q missing %q", errOut, tc.wantStderr)
			}
		})
	}
}

func TestVersionDetailedPartialWithoutNodeRBAC(t *testing.T) {
	path := writeFakeKubeconfig(t, fakeContext{name: "locked", server: (&fakeCluster{nodes: 2,
		systemDeployments: map[string]string{"coredns": "coredns:v1.11.1"},
		forbidden:         map[string]bool{"/api/v1/nodes": true}}).start(t)})
	out, errOut, err := runRoot(t, "version", "--detailed", "--per-cluster", "--strict", "--kubeconfig", path, "--cache-dir", t.TempDir())
	if err != nil {
		t.Fatalf("a partial cluster is not a failure: %v", err)
	}
	for _, w := range []string{`locked\s+\(partial\)\s+nodes: forbidden`, `locked\s+coredns\s+v1\.11\.1`} {
		if !regexp.MustCompile(w).MatchString(out) {
			t.Errorf("output does not match %q:\n%s", w, out)
		}
	}
	if !strings.Contains(errOut, "1 contexts returned partial results (nodes: forbidden): locked") {
		t.Errorf("stderr=%q", errOut)
	}
}
