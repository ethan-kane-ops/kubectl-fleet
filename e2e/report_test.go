//go:build e2e

package e2e

import (
	"testing"
	"time"
)

type reportDoc struct {
	Attention []struct {
		Context string `json:"context"`
		Kind    string `json:"kind"`
	} `json:"attention"`
	Clusters []struct {
		Context string `json:"context"`
		Error   string `json:"error"`
	} `json:"clusters"`
}

func TestE2E_Report(t *testing.T) {
	var doc reportDoc
	var code int
	crashloopIn := func(ctx string) bool {
		for _, a := range doc.Attention {
			if a.Context == ctx && a.Kind == "crashLoop" {
				return true
			}
		}
		return false
	}
	// As in TestE2E_Status, poll until the crashlooping pod is caught in
	// its back-off state.
	ok := eventually(crashloopWaitTimeout, 3*time.Second, func() bool {
		doc, code = runJSON[reportDoc](t, "report", "--contexts", "^kind-fleet-e2e-")
		return code == 2 && crashloopIn(contextB)
	})
	if !ok {
		t.Fatalf("report never flagged a crashloop in %s (exit %d): %+v\n%s",
			contextB, code, doc, describePods(contextB, "payments", "app=crashy"))
	}
	if crashloopIn(contextA) {
		t.Errorf("%s should have no crashloop item: %+v", contextA, doc.Attention)
	}
	if len(doc.Clusters) != 2 {
		t.Errorf("want 2 clusters, got %+v", doc.Clusters)
	}
}
