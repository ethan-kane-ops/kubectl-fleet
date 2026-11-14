package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
)

func TestEveryFleetCommandSharesSelectionFlags(t *testing.T) {
	root := NewRootCmd()
	for _, name := range []string{"contexts", "get", "status", "version"} {
		c, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatalf("find %s: %v", name, err)
		}
		for _, flag := range []string{"contexts", "exclude", "parallelism", "strict", "output", "no-headers", "version"} {
			if c.Flags().Lookup(flag) == nil {
				t.Errorf("%s is missing --%s", name, flag)
			}
		}
	}
}

func TestNoContextsLeftAfterExclude(t *testing.T) {
	for _, args := range [][]string{{"get", "pods"}, {"status"}, {"version"}} {
		c := NewRootCmd()
		c.SetOut(&bytes.Buffer{})
		c.SetErr(&bytes.Buffer{})
		c.SetArgs(append(args, "--exclude", ".", "--kubeconfig", tmpKubeconfig(t)))
		err := c.Execute()
		if err == nil || !strings.Contains(err.Error(), "no matching contexts") {
			t.Errorf("%v: want 'no matching contexts', got %v", args, err)
		}
	}
}

func TestIncludeCombinesRegexes(t *testing.T) {
	path := writeFakeKubeconfig(t,
		fakeContext{name: "prod-eu-1", server: "http://127.0.0.1:1"},
		fakeContext{name: "prod-us-1", server: "http://127.0.0.1:1"},
		fakeContext{name: "staging-1", server: "http://127.0.0.1:1"},
	)
	kubeFlags := newFlags(path)
	cfg, err := kubeconfig.Load(kubeFlags)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	tests := []struct {
		name     string
		contexts []string
		want     string
		selected []string
		wantErr  string
	}{
		{name: "none", want: "", selected: []string{"prod-eu-1", "prod-us-1", "staging-1"}},
		{name: "one", contexts: []string{"^prod-"}, want: "^prod-", selected: []string{"prod-eu-1", "prod-us-1"}},
		{name: "two", contexts: []string{"^prod-eu", "^prod-us"}, want: "(?:^prod-eu)|(?:^prod-us)", selected: []string{"prod-eu-1", "prod-us-1"}},
		{name: "bad", contexts: []string{"^ok", "("}, wantErr: `compile --contexts "("`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := fleetOptions{contexts: tt.contexts}
			got, err := o.include()
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("include() = %q, %v; want %q", got, err, tt.want)
			}
			refs, err := kubeconfig.Select(kubeFlags, cfg, got, "")
			if err != nil {
				t.Fatalf("select: %v", err)
			}
			var names []string
			for _, r := range refs {
				names = append(names, r.Name)
			}
			if strings.Join(names, ",") != strings.Join(tt.selected, ",") {
				t.Errorf("selected %v, want %v", names, tt.selected)
			}
		})
	}
}

func TestStatusRepeatedContexts(t *testing.T) {
	a := &fakeCluster{nodes: 1}
	b := &fakeCluster{nodes: 1}
	c := &fakeCluster{nodes: 1}
	path := writeFakeKubeconfig(t,
		fakeContext{name: "a", server: a.start(t)},
		fakeContext{name: "b", server: b.start(t)},
		fakeContext{name: "c", server: c.start(t)},
	)
	stdout, _, err := runRoot(t, "status", "--contexts", "^a$", "--contexts", "^b$", "--no-headers", "--kubeconfig", path, "--cache-dir", t.TempDir())
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if lines := strings.Count(stdout, "\n"); lines != 2 {
		t.Errorf("want 2 rows, got %d:\n%s", lines, stdout)
	}
}
