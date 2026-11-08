package cmd

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"sigs.k8s.io/yaml"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
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
	lacks := &fakeCluster{pods: []fakePod{{"team-b", "web"}, {"team-b", "other"}}}
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
		{"-A finds the name in any namespace", []string{"pods", "web", "-A", "-o", "name"}, "has/default/web\nlacks/team-b/web\n", ""},
		{"-A with the name nowhere is an error", []string{"pods", "nope", "-A"}, "", `pods "nope" not found in any reachable context`},
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

func TestListsArePaged(t *testing.T) {
	cl := &fakeCluster{}
	for i := range 600 {
		cl.pods = append(cl.pods, fakePod{"default", fmt.Sprintf("p%03d", i)})
	}
	path := writeFakeKubeconfig(t, fakeContext{name: "big", server: cl.start(t)})
	cases := []struct {
		name  string
		cmd   func() *cobra.Command
		args  []string
		check func(t *testing.T, out string)
	}{
		{"get", func() *cobra.Command { return newGetCmd(newFlags(path)) }, []string{"pods", "-o", "name"}, func(t *testing.T, out string) {
			if n := strings.Count(out, "\n"); n != 600 {
				t.Errorf("got %d pods want 600", n)
			}
		}},
		{"status", func() *cobra.Command { return newStatusCmd(newFlags(path)) }, []string{"-o", "json"}, func(t *testing.T, out string) {
			if !strings.Contains(out, `"podsTotal": 600`) {
				t.Errorf("want podsTotal 600 in:\n%s", out)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := len(cl.paths())
			c := tc.cmd()
			var out bytes.Buffer
			c.SetOut(&out)
			c.SetErr(&bytes.Buffer{})
			c.SetArgs(tc.args)
			if err := c.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			tc.check(t, out.String())
			pages := 0
			for _, p := range cl.paths()[before:] {
				if strings.Contains(p, "/pods?") && strings.Contains(p, "limit=500") {
					pages++
				}
			}
			if pages != 2 {
				t.Errorf("pod list requests with limit=500: %d want 2", pages)
			}
		})
	}
}

func TestGetStructuredOutputIsAListOfObjects(t *testing.T) {
	a := &fakeCluster{pods: []fakePod{{"default", "web"}}}
	b := &fakeCluster{pods: []fakePod{{"default", "web"}}}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "a", server: a.start(t)},
		fakeContext{name: "b", server: b.start(t)},
		fakeContext{name: "down", server: "http://127.0.0.1:1"},
	)
	for _, format := range []string{"json", "yaml"} {
		t.Run(format, func(t *testing.T) {
			c := newGetCmd(newFlags(path))
			var stdout bytes.Buffer
			c.SetOut(&stdout)
			c.SetErr(&bytes.Buffer{})
			c.SetArgs([]string{"pods", "-o", format})
			if err := c.Execute(); err != nil {
				t.Fatalf("Execute: %v", err)
			}
			var list struct {
				Kind  string `json:"kind"`
				Items []struct {
					Kind     string `json:"kind"`
					Metadata struct {
						Name        string            `json:"name"`
						Annotations map[string]string `json:"annotations"`
					} `json:"metadata"`
				} `json:"items"`
			}
			if err := yaml.Unmarshal(stdout.Bytes(), &list); err != nil {
				t.Fatalf("decode: %v\n%s", err, stdout.String())
			}
			if list.Kind != "List" {
				t.Errorf("kind=%q want List", list.Kind)
			}
			var got []string
			for _, it := range list.Items {
				if it.Kind != "Pod" || it.Metadata.Name != "web" {
					t.Errorf("unexpected item %+v", it)
				}
				got = append(got, it.Metadata.Annotations[ContextAnnotation])
			}
			if strings.Join(got, ",") != "a,b" {
				t.Errorf("item contexts=%v want [a b], failed context left out", got)
			}
		})
	}
}

func TestSchemaForResultsMatchesGroup(t *testing.T) {
	ok := func(group, resource string) []fleet.ClusterResult[fetchedResource] {
		return []fleet.ClusterResult[fetchedResource]{{Context: "c", Value: fetchedResource{group: group, resource: resource, namespaced: true}}}
	}
	failed := []fleet.ClusterResult[fetchedResource]{{Context: "c", Err: errors.New("down")}}
	cases := []struct {
		name      string
		kind      string
		results   []fleet.ClusterResult[fetchedResource]
		wantFirst string
	}{
		{"core pods get pod columns", "pods", ok("", "pods"), "READY"},
		{"metrics pods fall back to generic", "pods.metrics.k8s.io", ok("metrics.k8s.io", "pods"), "AGE"},
		{"apps deployments get deployment columns", "deploy", ok("apps", "deployments"), "READY"},
		{"no success, alias", "po", failed, "READY"},
		{"no success, other group", "nodes.metrics.k8s.io", failed, "AGE"},
		{"no success, matching group", "deployments.apps", failed, "READY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, _, _ := schemaForResults(tc.kind, tc.results)
			if s.Headers[1] != tc.wantFirst {
				t.Errorf("headers=%v want second column %s", s.Headers, tc.wantFirst)
			}
		})
	}
}

func TestGetReportsContextsResolvingAnotherGroup(t *testing.T) {
	a, b := &fakeCluster{widgetGroup: "a.example.io"}, &fakeCluster{widgetGroup: "b.example.io"}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "a", server: a.start(t)},
		fakeContext{name: "b", server: b.start(t)},
	)
	c := newGetCmd(newFlags(path))
	var stdout, stderr bytes.Buffer
	c.SetOut(&stdout)
	c.SetErr(&stderr)
	c.SetArgs([]string{"widgets", "--strict"})
	err := c.Execute()
	if err == nil || !strings.Contains(err.Error(), "1 of 2 contexts failed") {
		t.Fatalf("want b counted as failed under --strict, got %v", err)
	}
	want := "warn: b: widgets resolves to widgets.b.example.io here, not widgets.a.example.io"
	if !strings.Contains(stderr.String(), want) {
		t.Errorf("stderr=%q want %q", stderr.String(), want)
	}
}
