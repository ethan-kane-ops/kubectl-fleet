//go:build e2e

package e2e

import (
	"testing"
	"time"
)

func TestE2E_Status(t *testing.T) {
	var rows []map[string]string
	var code int

	// CrashLoopBackOff continuously cycles (kubelet retries, briefly clears
	// the state, re-enters backoff), so poll the actual CLI output until it
	// catches the crash-looping cluster in that state rather than trusting a
	// one-time precondition check elsewhere to still hold by the time this
	// runs.
	eventually(t, fixtureWaitTimeout, 3*time.Second, func() bool {
		rows, code = runJSON(t, "status", "--contexts", "^kind-fleet-e2e-")
		if code != 0 {
			return false
		}
		for _, r := range rows {
			if r["CONTEXT"] == contextB && r["CRASHLOOP"] != "" && r["CRASHLOOP"] != "0" {
				return true
			}
		}
		return false
	})

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
}
