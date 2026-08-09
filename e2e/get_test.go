//go:build e2e

package e2e

import "testing"

func TestE2E_Get(t *testing.T) {
	rows, code := runJSON(t, "get", "deploy", "api", "-n", "payments", "--contexts", "^kind-fleet-e2e-")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 (per-cluster errors shouldn't fail without --strict)", code)
	}

	var sawA bool
	for _, r := range rows {
		if r["CONTEXT"] != contextA {
			continue
		}
		sawA = true
		if r["NAME"] != "api" {
			t.Errorf("%s NAME = %q, want api", contextA, r["NAME"])
		}
		if r["READY"] != "2/2" {
			t.Errorf("%s READY = %q, want 2/2", contextA, r["READY"])
		}
	}
	if !sawA {
		t.Errorf("expected an api deployment row from %s, got %+v", contextA, rows)
	}

	// payments/api only exists on cluster A; cluster B should surface a
	// per-context <error> row rather than aborting the whole command.
	var sawBError bool
	for _, r := range rows {
		if r["CONTEXT"] == contextB && r["NAME"] == "<error>" {
			sawBError = true
		}
	}
	if !sawBError {
		t.Errorf("expected an <error> row from %s (api doesn't exist there), got %+v", contextB, rows)
	}
}
