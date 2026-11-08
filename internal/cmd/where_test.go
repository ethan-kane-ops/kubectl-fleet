package cmd

import (
	"bytes"
	"strings"
	"testing"
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
