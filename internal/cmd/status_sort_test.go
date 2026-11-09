package cmd

import (
	"testing"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/health"
)

func TestSortByPodsUsesRunningPods(t *testing.T) {
	rows := []contextSummary{
		{context: "busy-history", summary: health.Summary{PodsRunning: 5, PodsTotal: 50}},
		{context: "busy-now", summary: health.Summary{PodsRunning: 10, PodsTotal: 12}},
	}
	sortSummaries(rows, "pods")
	if rows[0].context != "busy-now" {
		t.Errorf("--sort-by pods should follow the PODS column (running), got %s first", rows[0].context)
	}
}
