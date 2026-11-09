package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// completions runs cobra's __complete entry point and returns the
// candidates (without descriptions) and the directive line.
func completions(t *testing.T, args ...string) (candidates []string, directive string) {
	t.Helper()
	out, _, err := runRoot(t, append([]string{"__complete"}, args...)...)
	if err != nil {
		t.Fatalf("__complete %v: %v", args, err)
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.HasPrefix(line, ":") {
			directive = line
			continue
		}
		name, _, _ := strings.Cut(line, "\t")
		candidates = append(candidates, name)
	}
	return candidates, directive
}

func TestCompleteContextNames(t *testing.T) {
	path := tmpKubeconfig(t)
	for _, flag := range []string{"--contexts", "--exclude", "--context"} {
		got, directive := completions(t, "get", "pods", "--kubeconfig", path, flag, "")
		if strings.Join(got, ",") != "prod,stage" || directive != ":4" {
			t.Errorf("%s: got %v %s, want [prod stage] :4 (no file completion)", flag, got, directive)
		}
	}
}

func TestCompleteFlagValues(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"status", "--sort-by", ""}, "nodes,pods,version,context"},
		{[]string{"get", "pods", "-o", ""}, "table,wide,json,yaml,name"},
		{[]string{"status", "--where", ""}, "version<,version>=,nodes>,nodes<,reachable,!reachable"},
	}
	for _, tc := range cases {
		if got, _ := completions(t, tc.args...); strings.Join(got, ",") != tc.want {
			t.Errorf("%v: got %v want %s", tc.args, got, tc.want)
		}
	}
}

func TestCompleteKinds(t *testing.T) {
	cl := &fakeCluster{}
	path := writeFakeKubeconfig(t, fakeContext{name: "c", server: cl.start(t)})
	got, _ := completions(t, "get", "--kubeconfig", path, "--cache-dir", t.TempDir(), "--context", "c", "")
	if !contains(got, "pods") || !contains(got, "nodes") {
		t.Errorf("kinds %v should include pods and nodes", got)
	}
}

func TestCompleteNamesDeadline(t *testing.T) {
	fast := &fakeCluster{pods: []fakePod{{"default", "web"}, {"default", "api"}}}
	slow := &fakeCluster{pods: []fakePod{{"default", "late"}}, delay: 10 * time.Second}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "fast", server: fast.start(t)},
		fakeContext{name: "slow", server: slow.start(t)},
	)
	start := time.Now()
	got, directive := completions(t, "get", "pods", "--kubeconfig", path, "--cache-dir", t.TempDir(), "--contexts", ".", "")
	if took := time.Since(start); took > 4*time.Second {
		t.Errorf("completion took %s; a slow cluster must not hold it past the deadline", took)
	}
	if strings.Join(got, ",") != "api,web" || directive != ":4" {
		t.Errorf("got %v %s, want the fast cluster's names [api web] :4", got, directive)
	}
}

func TestCompletionShim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the shim is a POSIX shell script")
	}
	bin := t.TempDir()
	build := exec.Command("go", "build", "-o", filepath.Join(bin, "kubectl-fleet"), "../../cmd/kubectl-fleet")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	shim, err := filepath.Abs("../../hack/kubectl_complete-fleet")
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(shim); err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("shim must exist and be executable: %v", err)
	}
	cmd := exec.Command(shim, "get", "pods", "--kubeconfig", tmpKubeconfig(t), "--contexts", "")
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("shim: %v", err)
	}
	if !strings.HasPrefix(string(out), "prod\nstage\n:4") {
		t.Errorf("kubectl fleet <TAB> output = %q", out)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestCompleteNamesMostlyDeadFleet(t *testing.T) {
	slow := &fakeCluster{delay: 10 * time.Second}
	fast := &fakeCluster{pods: []fakePod{{"default", "web"}}}
	slowURL := slow.start(t)
	var ctxs []fakeContext
	for i := range 300 {
		ctxs = append(ctxs, fakeContext{name: fmt.Sprintf("hung-%03d", i), server: slowURL})
	}
	ctxs = append(ctxs, fakeContext{name: "zz-fast", server: fast.start(t)})
	path := writeFakeKubeconfig(t, ctxs...)
	start := time.Now()
	got, _ := completions(t, "get", "pods", "--kubeconfig", path, "--cache-dir", t.TempDir(), "--contexts", ".", "")
	if took := time.Since(start); took > 4*time.Second {
		t.Errorf("took %s, past the deadline", took)
	}
	if strings.Join(got, ",") != "web" {
		t.Errorf("got %v; the one healthy context must still contribute among 300 hung ones", got)
	}
}
