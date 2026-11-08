//go:build e2e

package e2e

import (
	"testing"
	"time"
)

type statusRecord struct {
	Context       string `json:"context"`
	PodsCrashLoop int    `json:"podsCrashLoop"`
	Error         string `json:"error"`
}

func TestE2E_Status(t *testing.T) {
	var rows []statusRecord
	var code int

	// CrashLoopBackOff continuously cycles (kubelet retries, briefly clears
	// the state, re-enters backoff), so poll the actual CLI output until it
	// catches the crash-looping cluster in that state rather than trusting a
	// one-time precondition check elsewhere to still hold by the time this
	// runs.
	ok := eventually(crashloopWaitTimeout, 3*time.Second, func() bool {
		rows, code = runJSON[[]statusRecord](t, "status", "--contexts", "^kind-fleet-e2e-")
		if code != 0 {
			return false
		}
		for _, r := range rows {
			if r.Context == contextB && r.PodsCrashLoop > 0 {
				return true
			}
		}
		return false
	})
	if !ok {
		t.Fatalf("%s never reported podsCrashLoop>0 within %s; last rows: %+v\n%s",
			contextB, crashloopWaitTimeout, rows, describePods(contextB, "payments", "app=crashy"))
	}

	byCtx := map[string]statusRecord{}
	for _, r := range rows {
		byCtx[r.Context] = r
	}
	a, ok := byCtx[contextA]
	if !ok {
		t.Fatalf("missing row for %s: %+v", contextA, rows)
	}
	if a.Error != "" || a.PodsCrashLoop != 0 {
		t.Errorf("%s = %+v, want no error and podsCrashLoop 0", contextA, a)
	}
}
