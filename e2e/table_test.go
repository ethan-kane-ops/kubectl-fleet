//go:build e2e

package e2e

import (
	"regexp"
	"testing"
)

// TestE2E_GetTable checks table output against a real API server: by default
// the server renders the columns (kubectl's own), and --server-print=false
// falls back to the built-in printers, which must agree for deployments.
func TestE2E_GetTable(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "server-rendered deployment columns",
			args: []string{"get", "deploy", "-n", "payments"},
			want: []string{
				`(?m)^CONTEXT\s+NAMESPACE\s+NAME\s+READY\s+UP-TO-DATE\s+AVAILABLE\s+AGE\s*$`,
				`(?m)^` + contextA + `\s+payments\s+api\s+2/2\s+2\s+2\s+\S+\s*$`,
			},
		},
		{
			name: "server-rendered wide columns",
			args: []string{"get", "deploy", "-n", "payments", "-o", "wide"},
			want: []string{
				`CONTAINERS\s+IMAGES\s+SELECTOR`,
				contextA + `\s+payments\s+api\s+2/2\s+2\s+2\s+\S+\s+api\s+busybox:stable\s+app=api`,
			},
		},
		{
			name: "built-in columns agree",
			args: []string{"get", "deploy", "-n", "payments", "--server-print=false"},
			want: []string{
				`(?m)^CONTEXT\s+NAMESPACE\s+NAME\s+READY\s+UP-TO-DATE\s+AVAILABLE\s+AGE\s*$`,
				`(?m)^` + contextA + `\s+payments\s+api\s+2/2\s+2\s+2\s+\S+\s*$`,
			},
		},
		{
			name: "pods across namespaces carry the namespace from row metadata",
			args: []string{"get", "pods", "-A", "-l", "app=api"},
			want: []string{
				`(?m)^CONTEXT\s+NAMESPACE\s+NAME\s+READY\s+STATUS\s+RESTARTS\s+AGE\s*$`,
				`(?m)^` + contextA + `\s+payments\s+api-\S+\s+1/1\s+Running\s+0\s+\S+\s*$`,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, stderr, code := run(t, kubeconfig, append(tc.args, "--contexts", "^kind-fleet-e2e-")...)
			if code != 0 {
				t.Fatalf("exit %d: %s", code, stderr)
			}
			for _, w := range tc.want {
				if !regexp.MustCompile(w).MatchString(out) {
					t.Errorf("output does not match %q:\n%s", w, out)
				}
			}
		})
	}
}
