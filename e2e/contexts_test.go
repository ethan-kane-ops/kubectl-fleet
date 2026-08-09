//go:build e2e

package e2e

import "testing"

func TestE2E_ContextsCheck(t *testing.T) {
	rows, code := runJSON(t, "contexts", "--check", "--filter", "^kind-fleet-e2e-")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 contexts, got %d: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r["REACHABLE"] != "yes" {
			t.Errorf("context %s: REACHABLE = %q, want yes (ERROR=%q)", r["CONTEXT"], r["REACHABLE"], r["ERROR"])
		}
	}
}
