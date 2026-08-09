//go:build e2e

package e2e

import "testing"

func TestE2E_Status(t *testing.T) {
	rows, code := runJSON(t, "status", "--contexts", "^kind-fleet-e2e-")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	byCtx := map[string]map[string]string{}
	for _, r := range rows {
		byCtx[r["CONTEXT"]] = r
	}
	a, ok := byCtx[contextA]
	if !ok {
		t.Fatalf("missing row for %s: %+v", contextA, rows)
	}
	if a["CRASHLOOP"] != "0" {
		t.Errorf("%s CRASHLOOP = %q, want 0", contextA, a["CRASHLOOP"])
	}

	b, ok := byCtx[contextB]
	if !ok {
		t.Fatalf("missing row for %s: %+v", contextB, rows)
	}
	if b["CRASHLOOP"] == "0" || b["CRASHLOOP"] == "" {
		t.Errorf("%s CRASHLOOP = %q, want >0 (payments/crashy should be looping)", contextB, b["CRASHLOOP"])
	}
}
