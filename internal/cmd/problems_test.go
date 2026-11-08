package cmd

import (
	"strings"
	"testing"
)

func TestProblemRow(t *testing.T) {
	pod := []string{"NAME", "READY", "STATUS", "RESTARTS", "AGE"}
	cases := []struct {
		name        string
		columns     []string
		cells       []string
		wantProblem bool
		wantOK      bool
	}{
		{"running and ready", pod, []string{"web", "1/1", "Running", "0", "1d"}, false, true},
		{"finished job pod", pod, []string{"job", "0/1", "Completed", "0", "1d"}, false, true},
		{"running but not ready", pod, []string{"web", "1/2", "Running", "0", "1d"}, true, true},
		{"crashlooping", pod, []string{"web", "0/1", "CrashLoopBackOff", "9", "1d"}, true, true},
		{"pending", pod, []string{"web", "0/1", "Pending", "0", "1d"}, true, true},
		{"cordoned node", []string{"NAME", "STATUS", "ROLES"}, []string{"n1", "Ready,SchedulingDisabled", "<none>"}, false, true},
		{"not ready node", []string{"NAME", "STATUS", "ROLES"}, []string{"n1", "NotReady", "<none>"}, true, true},
		{"node under pressure", []string{"NAME", "STATUS"}, []string{"n1", "Ready,DiskPressure"}, true, true},
		{"deployment short", []string{"NAME", "READY", "UP-TO-DATE"}, []string{"api", "2/3", "3"}, true, true},
		{"deployment full", []string{"NAME", "READY", "UP-TO-DATE"}, []string{"api", "3/3", "3"}, false, true},
		{"daemonset short", []string{"NAME", "DESIRED", "CURRENT", "READY"}, []string{"ds", "3", "3", "2"}, true, true},
		{"daemonset full", []string{"NAME", "DESIRED", "CURRENT", "READY"}, []string{"ds", "3", "3", "3"}, false, true},
		{"pvc bound", []string{"NAME", "STATUS", "VOLUME"}, []string{"data", "Bound", "pv-1"}, false, true},
		{"no readiness columns", []string{"NAME", "DATA", "AGE"}, []string{"cm", "2", "1d"}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problem, ok := problemRow(tc.columns, tc.cells)
			if problem != tc.wantProblem || ok != tc.wantOK {
				t.Errorf("problemRow=%v,%v want %v,%v", problem, ok, tc.wantProblem, tc.wantOK)
			}
		})
	}
}

func TestGetProblems(t *testing.T) {
	a := &fakeCluster{pods: []fakePod{{"default", "web"}, {"default", "worker"}}, podStatus: map[string]string{"worker": "CrashLoopBackOff"}}
	b := &fakeCluster{pods: []fakePod{{"default", "api"}}, widgetGroup: "example.io"}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "a", server: a.start(t)},
		fakeContext{name: "b", server: b.start(t), namespace: "default"},
	)
	cases := []struct {
		name       string
		args       []string
		wantOut    []string
		notWantOut []string
		wantErr    string
	}{
		{"only unhealthy rows", []string{"get", "pods", "--problems"}, []string{"worker", "CrashLoopBackOff"}, []string{"web", "api"}, ""},
		{"kind without readiness columns", []string{"get", "widgets", "--problems", "--contexts", "^b$"}, nil, nil, "--problems needs a READY or STATUS column; widgets has neither"},
		{"structured output refused", []string{"get", "pods", "--problems", "-o", "json"}, nil, nil, "--problems works with table, wide and name output"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := runRoot(t, append(tc.args, "--kubeconfig", path, "--cache-dir", t.TempDir())...)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for _, w := range tc.wantOut {
				if !strings.Contains(out, w) {
					t.Errorf("missing %q:\n%s", w, out)
				}
			}
			for _, w := range tc.notWantOut {
				if strings.Contains(out, w) {
					t.Errorf("unexpected %q:\n%s", w, out)
				}
			}
		})
	}
}
