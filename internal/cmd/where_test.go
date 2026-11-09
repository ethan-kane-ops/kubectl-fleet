package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// whereFleet is three contexts: old (v1.29.8, 2 nodes), new (v1.31.0,
// 5 nodes) and dead (nothing listening).
func whereFleet(t *testing.T) (string, *fakeCluster) {
	t.Helper()
	newer := &fakeCluster{gitVersion: "v1.31.0", nodes: 5}
	return writeFakeKubeconfig(t,
		fakeContext{name: "dead", server: "http://127.0.0.1:1"},
		fakeContext{name: "new", server: newer.start(t)},
		fakeContext{name: "old", server: (&fakeCluster{gitVersion: "v1.29.8-eks-a737599", nodes: 2}).start(t)},
	), newer
}

func runRoot(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	c := NewRootCmd()
	var out, errOut bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errOut)
	c.SetArgs(args)
	err = c.Execute()
	return out.String(), errOut.String(), err
}

func TestWhereSelection(t *testing.T) {
	path, _ := whereFleet(t)
	cases := []struct {
		name       string
		args       []string
		wantErr    string
		wantOut    []string
		notWantOut []string
		wantStderr []string
	}{
		{
			name:       "version filter keeps old, dead is a failure",
			args:       []string{"status", "--where", "version<1.30"},
			wantOut:    []string{"old"},
			notWantOut: []string{"new"},
			wantStderr: []string{"dead"},
		},
		{
			name:    "a failure still fails --strict",
			args:    []string{"status", "--where", "version<1.30", "--strict"},
			wantErr: "1 of",
		},
		{
			name:       "reachable skips dead without failing --strict",
			args:       []string{"status", "--where", "reachable", "--where", "version<1.30", "--strict"},
			wantOut:    []string{"old"},
			notWantOut: []string{"new", "dead"},
			wantStderr: []string{"skipped 1 unreachable"},
		},
		{
			name:    "!reachable is only for contexts",
			args:    []string{"get", "pods", "--where", "!reachable"},
			wantErr: "contexts --where '!reachable'",
		},
		{
			name:       "contexts lists the unreachable ones",
			args:       []string{"contexts", "--where", "!reachable"},
			wantOut:    []string{"dead", "no"},
			notWantOut: []string{"old", "new"},
		},
		{
			name:    "nothing left after --where",
			args:    []string{"version", "--where", "reachable", "--where", "version>2.0"},
			wantErr: "no matching contexts",
		},
		{
			name:    "bad expression",
			args:    []string{"status", "--where", "colour=blue"},
			wantErr: `unknown attribute "colour"`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, err := runRoot(t, append(tc.args, "--kubeconfig", path, "--cache-dir", t.TempDir())...)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Execute: %v\nstderr: %s", err, errOut)
			}
			for _, w := range tc.wantOut {
				if !strings.Contains(out, w) {
					t.Errorf("stdout missing %q:\n%s", w, out)
				}
			}
			for _, w := range tc.notWantOut {
				if strings.Contains(out, w) {
					t.Errorf("stdout should not contain %q:\n%s", w, out)
				}
			}
			for _, w := range tc.wantStderr {
				if !strings.Contains(errOut, w) {
					t.Errorf("stderr missing %q:\n%s", w, errOut)
				}
			}
		})
	}
}

func TestWhereNodesCountsWithMetadataList(t *testing.T) {
	path, newer := whereFleet(t)
	out, _, err := runRoot(t, "version", "--where", "reachable", "--where", "nodes>2", "--kubeconfig", path, "--cache-dir", t.TempDir())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out, "new") || strings.Contains(out, "old") {
		t.Errorf("nodes>2 should keep only new (5 nodes):\n%s", out)
	}
	if got := newer.acceptFor("/api/v1/nodes"); !strings.Contains(got, "as=PartialObjectMetadataList") {
		t.Errorf("node count should use a metadata-only list, Accept=%q", got)
	}
}

func TestNoProbeWithoutWhere(t *testing.T) {
	cl := &fakeCluster{pods: []fakePod{{"default", "web"}}}
	path := writeFakeKubeconfig(t, fakeContext{name: "c", server: cl.start(t)})
	if _, _, err := runRoot(t, "get", "pods", "--kubeconfig", path, "--cache-dir", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if requested(cl, "/version") {
		t.Errorf("get without --where should not probe /version: %v", cl.paths())
	}
}

// partialFleet is one healthy context ("up") and one with nothing listening
// ("down").
func partialFleet(t *testing.T) string {
	t.Helper()
	up := &fakeCluster{pods: []fakePod{{"default", "web"}}, nodes: 1}
	return writeFakeKubeconfig(t,
		fakeContext{name: "down", server: "http://127.0.0.1:1"},
		fakeContext{name: "up", server: up.start(t)},
	)
}

func TestAllContextsFailedExitsNonZero(t *testing.T) {
	path := tmpKubeconfigUnreachable(t)
	for _, args := range [][]string{{"get", "pods"}, {"status"}, {"version"}, {"version", "--detailed"}} {
		_, _, err := runRoot(t, append(args, "--kubeconfig", path, "--cache-dir", t.TempDir())...)
		if err == nil || !strings.Contains(err.Error(), "all 1 contexts failed") {
			t.Errorf("%v: want 'all 1 contexts failed', got %v", args, err)
		}
	}
	// contexts reports reachability; an all-down answer is still an answer.
	if _, _, err := runRoot(t, "contexts", "--check", "--kubeconfig", path); err != nil {
		t.Errorf("contexts --check should not fail without --strict: %v", err)
	}
}

func TestContextsWhereHonoursTimeout(t *testing.T) {
	slow := &fakeCluster{delay: 5 * time.Second}
	path := writeFakeKubeconfig(t, fakeContext{name: "slow", server: slow.start(t)})
	start := time.Now()
	out, _, err := runRoot(t, "contexts", "--where", "!reachable", "--timeout", "1s", "--kubeconfig", path, "--cache-dir", t.TempDir())
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("took %s; --timeout 1s should bound each --where probe", took)
	}
	if err != nil || !strings.Contains(out, "slow") {
		t.Errorf("a context slower than --timeout counts as unreachable: err=%v out=%s", err, out)
	}
}

func TestWhereReachableEdgeCases(t *testing.T) {
	denied := &fakeCluster{forbidden: map[string]bool{"/version": true}}
	cases := []struct {
		name       string
		ctxs       []fakeContext
		args       []string
		wantErr    string
		wantStderr string
	}{
		{
			name:    "auth failure is a failure, not unreachable",
			ctxs:    []fakeContext{{name: "denied", server: denied.start(t)}, {name: "ok", server: (&fakeCluster{}).start(t)}},
			args:    []string{"status", "--where", "reachable", "--strict"},
			wantErr: "1 of 2 contexts failed",
		},
		{
			name:    "every context unreachable says so",
			ctxs:    []fakeContext{{name: "down", server: "http://127.0.0.1:1"}},
			args:    []string{"status", "--where", "reachable"},
			wantErr: "all 1 selected contexts are unreachable",
		},
		{
			name:       "nothing down is a successful answer",
			ctxs:       []fakeContext{{name: "ok", server: (&fakeCluster{}).start(t)}},
			args:       []string{"contexts", "--where", "!reachable"},
			wantStderr: "no unreachable contexts",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFakeKubeconfig(t, tc.ctxs...)
			_, errOut, err := runRoot(t, append(tc.args, "--kubeconfig", path, "--cache-dir", t.TempDir())...)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(errOut, tc.wantStderr) {
				t.Errorf("stderr %q missing %q", errOut, tc.wantStderr)
			}
		})
	}
}
