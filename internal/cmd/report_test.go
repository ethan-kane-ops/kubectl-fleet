package cmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type reportJSON struct {
	Selection struct {
		Contexts []string `json:"contexts"`
	} `json:"selection"`
	Attention []struct {
		Context string `json:"context"`
		Kind    string `json:"kind"`
		Message string `json:"message"`
	} `json:"attention"`
	Clusters []struct {
		Context string `json:"context"`
		Error   string `json:"error"`
	} `json:"clusters"`
}

func runReport(t *testing.T, path string, args ...string) (string, string, error) {
	t.Helper()
	return runRoot(t, append(append([]string{"report"}, args...), "--kubeconfig", path, "--cache-dir", t.TempDir())...)
}

func exitCode(err error) int {
	var ee *ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &ee):
		return ee.Code
	}
	return 1
}

func TestReportAllClear(t *testing.T) {
	a, b := &fakeCluster{nodes: 2, pods: []fakePod{{"default", "web"}}}, &fakeCluster{nodes: 1}
	path := writeFakeKubeconfig(t, fakeContext{name: "a", server: a.start(t)}, fakeContext{name: "b", server: b.start(t)})
	stdout, _, err := runReport(t, path, "-o", "json")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	var r reportJSON
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout)
	}
	if len(r.Clusters) != 2 || len(r.Attention) != 0 {
		t.Errorf("want 2 clusters and no attention: %+v", r)
	}
}

func TestReportAttentionExit(t *testing.T) {
	sick := &fakeCluster{nodes: 3, notReadyNodes: 1}
	path := writeFakeKubeconfig(t, fakeContext{name: "sick", server: sick.start(t)}, fakeContext{name: "down", server: "http://127.0.0.1:1"})
	stdout, _, err := runReport(t, path)
	if code := exitCode(err); code != 2 {
		t.Fatalf("exit %d (%v), want 2", code, err)
	}
	for _, want := range []string{"2 ITEMS NEED ATTENTION", "1 of 3 nodes NotReady", "context failed: connection refused"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("report lacks %q:\n%s", want, stdout)
		}
	}
	if strings.Contains(stdout, "127.0.0.1") || strings.Contains(stdout, "http") {
		t.Errorf("report leaks a server address:\n%s", stdout)
	}
}

func TestReportAllFailed(t *testing.T) {
	stdout, _, err := runReport(t, tmpKubeconfigUnreachable(t))
	if code := exitCode(err); code != 2 {
		t.Fatalf("exit %d (%v), want 2", code, err)
	}
	if !strings.Contains(stdout, "context failed") {
		t.Errorf("report should still be written:\n%s", stdout)
	}
}

func TestReportPodsForbidden(t *testing.T) {
	c := &fakeCluster{nodes: 1, forbidden: map[string]bool{"/api/v1/pods": true}}
	path := writeFakeKubeconfig(t, fakeContext{name: "c", server: c.start(t)})
	stdout, _, err := runReport(t, path)
	if code := exitCode(err); code != 2 {
		t.Fatalf("exit %d (%v), want 2", code, err)
	}
	if !strings.Contains(stdout, "could not inspect pods: forbidden") {
		t.Errorf("missing forbidden item:\n%s", stdout)
	}
}

func TestReportPodListsArePhaseFiltered(t *testing.T) {
	c := &fakeCluster{nodes: 2, pods: []fakePod{{"default", "a"}}}
	path := writeFakeKubeconfig(t, fakeContext{name: "c", server: c.start(t)})
	if _, _, err := runReport(t, path); err != nil {
		t.Fatalf("report: %v", err)
	}
	nodes := 0
	var pods []string
	for _, p := range c.paths() {
		switch {
		case strings.HasPrefix(p, "/api/v1/nodes?"):
			nodes++
		case strings.HasPrefix(p, "/api/v1/pods?"):
			pods = append(pods, p)
		}
	}
	if nodes != 1 || len(pods) != 4 {
		t.Fatalf("want one node list and four pod lists, got %d and %v", nodes, pods)
	}
	tables := 0
	for _, p := range pods {
		if !strings.Contains(p, "fieldSelector=status.phase") {
			t.Errorf("pod list without a phase selector: %s", p)
		}
		if strings.Contains(p, "includeObject=None") {
			tables++
		}
	}
	if tables != 2 {
		t.Errorf("want the Running and other lists as cells-only tables, got %d: %v", tables, pods)
	}
}

func TestReportFlagValidation(t *testing.T) {
	c := &fakeCluster{nodes: 1}
	path := writeFakeKubeconfig(t, fakeContext{name: "c", server: c.start(t)})
	for _, args := range [][]string{{"--expect-version", "latest"}, {"-o", "wide"}, {"--pending-for", "0"}} {
		_, _, err := runReport(t, path, args...)
		if exitCode(err) != 1 {
			t.Errorf("%v: want a flag error, got %v", args, err)
		}
	}
	if n := len(c.paths()); n != 0 {
		t.Errorf("flag errors must not contact clusters, saw %v", c.paths())
	}
}

func TestReportRepeatedContexts(t *testing.T) {
	a, b, c := &fakeCluster{nodes: 1}, &fakeCluster{nodes: 1}, &fakeCluster{nodes: 1}
	path := writeFakeKubeconfig(t, fakeContext{name: "a", server: a.start(t)}, fakeContext{name: "b", server: b.start(t)}, fakeContext{name: "c", server: c.start(t)})
	stdout, _, err := runReport(t, path, "--contexts", "^a$", "--contexts", "^b$", "-o", "json")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	var r reportJSON
	if err := json.Unmarshal([]byte(stdout), &r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(r.Clusters) != 2 || strings.Join(r.Selection.Contexts, ",") != "^a$,^b$" {
		t.Errorf("unexpected selection: %+v", r)
	}
}

func TestReportSkipUnreachableKeepsSkipped(t *testing.T) {
	up := &fakeCluster{nodes: 1}
	path := writeFakeKubeconfig(t, fakeContext{name: "up", server: up.start(t)}, fakeContext{name: "down", server: "http://127.0.0.1:1"})
	stdout, _, err := runReport(t, path, "--skip-unreachable")
	if code := exitCode(err); code != 2 {
		t.Fatalf("exit %d (%v), want 2", code, err)
	}
	if !strings.Contains(stdout, "unreachable (skipped by --skip-unreachable)") {
		t.Errorf("skipped context must appear in the report:\n%s", stdout)
	}
}

func TestReportSkipUnreachableAllSkipped(t *testing.T) {
	stdout, _, err := runReport(t, tmpKubeconfigUnreachable(t), "--skip-unreachable")
	if code := exitCode(err); code != 2 {
		t.Fatalf("exit %d (%v), want 2 with a written report", code, err)
	}
	if !strings.Contains(stdout, "unreachable (skipped by --skip-unreachable)") {
		t.Errorf("all-skipped report must list the skipped contexts:\n%s", stdout)
	}
}

func TestReportHelpAlertRecipe(t *testing.T) {
	if strings.Contains(reportExample, "|| [ $? -eq 2 ] &&") {
		t.Error("the alert recipe notifies on exit 0; use '; [ $? -eq 2 ] &&'")
	}
}
