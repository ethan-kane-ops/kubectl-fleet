package cmd

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
)

func TestReportFailures(t *testing.T) {
	timeout := fmt.Errorf("list pods: %w", errDeadline)
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("rbac"))
	var many []fleet.Failure
	for _, n := range []string{"prod-eu-3", "prod-eu-7", "prod-eu-1", "prod-eu-2", "prod-eu-4", "prod-eu-5", "prod-eu-6", "prod-eu-8"} {
		many = append(many, fleet.Failure{Context: n, Err: timeout})
	}
	for _, n := range []string{"dev-b", "dev-a", "dev-c", "dev-d"} {
		many = append(many, fleet.Failure{Context: n, Err: forbidden})
	}
	cases := []struct {
		name     string
		total    int
		failures []fleet.Failure
		skipped  int
		detailed bool
		want     string
	}{
		{"none", 5, nil, 0, false, ""},
		{"grouped brief", 200, many, 0, false,
			"warn: 12 of 200 contexts failed: 8 timed out (prod-eu-1, prod-eu-2, +6), 4 forbidden (dev-a, dev-b, +2). Use --errors for details.\n"},
		{"single", 3, []fleet.Failure{{Context: "down", Err: errors.New("boom")}}, 0, false,
			"warn: 1 of 3 contexts failed: 1 other (down). Use --errors for details.\n"},
		{"skipped only", 3, nil, 2, false, "warn: skipped 2 unreachable contexts (--where reachable).\n"},
		{"detailed", 3, []fleet.Failure{{Context: "b", Err: errors.New("two")}, {Context: "a", Err: errors.New("one")}}, 1, true,
			"warn: a: one\nwarn: b: two\nwarn: skipped 1 unreachable contexts (--where reachable).\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b strings.Builder
			reportFailures(&b, tc.total, tc.failures, tc.skipped, tc.detailed)
			if b.String() != tc.want {
				t.Errorf("got  %q\nwant %q", b.String(), tc.want)
			}
		})
	}
}

var errDeadline = deadlineErr{}

type deadlineErr struct{}

func (deadlineErr) Error() string   { return "context deadline exceeded" }
func (deadlineErr) Timeout() bool   { return true }
func (deadlineErr) Temporary() bool { return true }

func TestGetErrorsBriefByDefault(t *testing.T) {
	ok := &fakeCluster{pods: []fakePod{{"default", "web"}}}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "down", server: "http://127.0.0.1:1"},
		fakeContext{name: "up", server: ok.start(t)},
	)
	cases := []struct {
		name          string
		args          []string
		wantRow       bool
		wantStderr    string
		notWantStderr string
	}{
		{"default: summary, no error row", []string{"get", "pods"}, false, "1 of 2 contexts failed: 1 connection refused (down). Use --errors for details.", "warn: down:"},
		{"--errors: row and full message", []string{"get", "pods", "--errors"}, true, "warn: down:", "Use --errors"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, errOut, err := runRoot(t, append(tc.args, "--kubeconfig", path, "--cache-dir", t.TempDir())...)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(out, "<error>"); got != tc.wantRow {
				t.Errorf("<error> row present=%v want %v:\n%s", got, tc.wantRow, out)
			}
			if tc.wantRow && !strings.Contains(strings.SplitN(out, "\n", 2)[0], "ERROR") {
				t.Errorf("--errors should show the ERROR column without -o wide:\n%s", out)
			}
			if !strings.Contains(errOut, tc.wantStderr) || (tc.notWantStderr != "" && strings.Contains(errOut, tc.notWantStderr)) {
				t.Errorf("stderr=%q want %q without %q", errOut, tc.wantStderr, tc.notWantStderr)
			}
		})
	}
}
