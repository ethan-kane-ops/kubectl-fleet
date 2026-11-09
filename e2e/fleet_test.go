//go:build e2e

package e2e

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestE2E_Selection covers --exclude and --where against real API servers
// (kind runs a current Kubernetes, so version>=1.30 matches both clusters).
func TestE2E_Selection(t *testing.T) {
	mixed := mixedKubeconfig(t)
	cases := []struct {
		name       string
		args       []string
		wantCode   int
		want       []string
		notWant    []string
		wantStderr string
	}{
		{name: "exclude", args: []string{"status", "--exclude", contextB},
			want: []string{contextA}, notWant: []string{contextB}},
		{name: "where version matches both", args: []string{"status", "--where", "reachable", "--where", "version>=1.30"},
			want: []string{contextA, contextB}, wantStderr: "skipped 1 unreachable"},
		{name: "where version matches none", args: []string{"status", "--where", "reachable", "--where", "version<1.20"},
			wantCode: 1, wantStderr: "no matching contexts"},
		{name: "where nodes", args: []string{"status", "--where", "reachable", "--where", "nodes>=1"},
			want: []string{contextA, contextB}},
		{name: "unreachable listed by contexts", args: []string{"contexts", "--where", "!reachable"},
			want: []string{"kind-fleet-e2e-unreachable"}, notWant: []string{contextA}},
		{name: "brief error summary", args: []string{"version"},
			want: []string{contextA, contextB}, notWant: []string{"<error>"},
			wantStderr: "1 of 3 contexts failed: 1 connection refused (kind-fleet-e2e-unreachable). Use --errors for details."},
		{name: "--errors detail", args: []string{"version", "--errors"},
			want: []string{"<error>", "ERROR"}, wantStderr: "warn: kind-fleet-e2e-unreachable:"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, code := run(t, mixed, append(tc.args, "--contexts", "^kind-fleet-e2e-")...)
			if code != tc.wantCode {
				t.Fatalf("exit %d, want %d; stderr: %s", code, tc.wantCode, stderr)
			}
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("stdout missing %q:\n%s", w, out)
				}
			}
			for _, w := range tc.notWant {
				if strings.Contains(out, w) {
					t.Errorf("stdout should not contain %q:\n%s", w, out)
				}
			}
			if tc.wantStderr != "" && !strings.Contains(stderr, tc.wantStderr) {
				t.Errorf("stderr missing %q:\n%s", tc.wantStderr, stderr)
			}
		})
	}
}

// TestE2E_Problems finds the crashlooping workload on cluster B with both
// status --problems and get --problems, and nothing on cluster A.
func TestE2E_Problems(t *testing.T) {
	var status, pods string
	ok := eventually(crashloopWaitTimeout, 3*time.Second, func() bool {
		status, _, _ = run(t, kubeconfig, "status", "--problems", "--contexts", "^kind-fleet-e2e-")
		pods, _, _ = run(t, kubeconfig, "get", "pods", "-A", "--problems", "--contexts", "^kind-fleet-e2e-")
		return strings.Contains(status, contextB) && strings.Contains(pods, "crashy")
	})
	if !ok {
		t.Fatalf("never saw %s and its crashy pod as problems\nstatus:\n%s\npods:\n%s\n%s", contextB, status, pods,
			describePods(contextB, "payments", "app=crashy"))
	}
	if strings.Contains(status, contextA) {
		t.Errorf("healthy %s should not be listed by status --problems:\n%s", contextA, status)
	}
	if strings.Contains(pods, " api-") {
		t.Errorf("healthy api pods should not be listed by get --problems:\n%s", pods)
	}
}

// TestE2E_VersionDetailed reads real node and kube-system versions from kind.
func TestE2E_VersionDetailed(t *testing.T) {
	out, stderr, code := run(t, kubeconfig, "version", "--detailed", "--per-cluster", "--contexts", "^kind-fleet-e2e-")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	for _, want := range []string{
		`(?m)^` + contextA + `\s+kube-apiserver\s+v1\.\d+\.\d+`,
		`(?m)^` + contextA + `\s+kubelet\s+v1\.\d+\.\d+\s+1$`,
		`(?m)^` + contextA + `\s+container-runtime\s+containerd://\S+\s+1$`,
		`(?m)^` + contextA + `\s+coredns\s+v\S+`,
		`(?m)^` + contextA + `\s+kube-proxy\s+v\S+`,
		`(?m)^` + contextB + `\s+kindnet\s+\S+`,
	} {
		if !regexp.MustCompile(want).MatchString(out) {
			t.Errorf("output does not match %q:\n%s", want, out)
		}
	}
	spread, _, _ := run(t, kubeconfig, "version", "--detailed", "--contexts", "^kind-fleet-e2e-")
	if !regexp.MustCompile(`(?m)^kubelet\s+v1\.\d+\.\d+\s+2$`).MatchString(spread) {
		t.Errorf("spread should show one kubelet version on both clusters:\n%s", spread)
	}
}
