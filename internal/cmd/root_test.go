package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRootRejectsSingleClusterFlags(t *testing.T) {
	path := tmpKubeconfig(t)
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"get", "pods", "--server", "https://x"}, "--server"},
		{[]string{"status", "--context", "prod"}, "--context"},
		{[]string{"contexts", "--cluster", "c-prod"}, "--cluster"},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			c := NewRootCmd()
			c.SetOut(&bytes.Buffer{})
			c.SetErr(&bytes.Buffer{})
			c.SetArgs(append(tc.args, "--kubeconfig", path))
			err := c.Execute()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error naming %s, got %v", tc.want, err)
			}
		})
	}
}
