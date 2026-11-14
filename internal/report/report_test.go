package report

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/health"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/inventory"
)

func healthy(version string) *Cluster {
	return &Cluster{
		ControlPlane: version, NodesReady: 3, NodesTotal: 3,
		Kubelets:      []inventory.Count{{Value: version, N: 3}},
		Zones:         []inventory.Count{{Value: "eu-west-1a", N: 3}},
		InstanceTypes: []inventory.Count{{Value: "m7g.large", N: 3}},
		Provisioners:  []Provisioner{{Kind: "karpenter", Name: "general", Nodes: 3}},
		PodsRunning:   10, PodsTotal: 10,
	}
}

func TestBuildAttention(t *testing.T) {
	notReady := healthy("v1.30.4")
	notReady.NodesReady = 2
	crash := healthy("v1.30.4")
	crash.PodsCrashLoop, crash.CrashLoopNS = 3, []health.NamespaceNoise{{Namespace: "payments", Unhealthy: 2}, {Namespace: "web", Unhealthy: 1}}
	stuck := healthy("v1.30.4")
	stuck.PodsPending, stuck.PodsStuckPending, stuck.StuckPendingNS = 2, 2, []health.NamespaceNoise{{Namespace: "batch", Unhealthy: 2}}
	old := healthy("v1.29.8")
	forbidden := healthy("v1.30.4")
	forbidden.PodsForbidden = true
	forbidden.PodsCrashLoop = 5 // not trustworthy without the list; must be ignored
	partial := healthy("v1.30.4")
	partial.Partial = []string{"kube-system deployments: forbidden"}
	empty := &Cluster{ControlPlane: "v1.30.4"}

	r := Build(t0, Selection{ExpectVersion: "1.30", PendingFor: "5m"}, []Result{
		{Context: "z-down", Cause: "connection refused"},
		{Context: "a-notready", Cluster: notReady},
		{Context: "b-crash", Cluster: crash},
		{Context: "c-stuck", Cluster: stuck},
		{Context: "d-old", Cluster: old},
		{Context: "e-forbidden", Cluster: forbidden},
		{Context: "f-partial", Cluster: partial},
		{Context: "g-empty", Cluster: empty},
	})
	want := []Item{
		{Context: "z-down", Kind: KindFailed, Message: "context failed: connection refused"},
		{Context: "e-forbidden", Kind: KindUninspected, Message: "could not inspect pods: forbidden"},
		{Context: "a-notready", Kind: KindNotReady, Message: "1 of 3 nodes NotReady", Count: 1},
		{Context: "b-crash", Kind: KindCrashLoop, Message: "3 crashlooping pods (payments(2), web(1))", Count: 3},
		{Context: "c-stuck", Kind: KindStuckPending, Message: "2 pods pending longer than 5m (batch(2))", Count: 2},
		{Context: "d-old", Kind: KindVersion, Message: "control plane v1.29.8 is not 1.30"},
		{Context: "d-old", Kind: KindVersion, Message: "kubelets not on 1.30: v1.29.8(3)", Count: 3},
		{Context: "f-partial", Kind: KindPartial, Message: "partial data: kube-system deployments: forbidden"},
	}
	if !reflect.DeepEqual(r.Attention, want) {
		t.Errorf("attention:\n got %+v\nwant %+v", r.Attention, want)
	}
	if !r.NeedsAttention() {
		t.Error("NeedsAttention should be true")
	}
	if r.Clusters[0].Context != "a-notready" || r.Clusters[len(r.Clusters)-1].Context != "z-down" || r.Clusters[len(r.Clusters)-1].Error != "connection refused" {
		t.Errorf("clusters should be sorted by name with failures carrying their cause: %+v", r.Clusters)
	}
}

func TestBuildNoVersionRuleWithoutExpect(t *testing.T) {
	r := Build(t0, Selection{PendingFor: "5m"}, []Result{{Context: "old", Cluster: healthy("v1.20.0")}})
	if len(r.Attention) != 0 || r.NeedsAttention() {
		t.Errorf("want no attention, got %+v", r.Attention)
	}
}

func TestBuildPartialOnlyIsClear(t *testing.T) {
	c := healthy("v1.30.4")
	c.Partial = []string{"kube-system daemonsets: forbidden"}
	r := Build(t0, Selection{PendingFor: "5m"}, []Result{{Context: "a", Cluster: c}})
	if len(r.Attention) != 1 || r.NeedsAttention() {
		t.Errorf("partial data is informational: %+v", r.Attention)
	}
}

func TestBuildSummary(t *testing.T) {
	a, b := healthy("v1.30.4"), healthy("v1.29.8")
	a.InstanceTypes = nil
	for i := range 12 {
		a.InstanceTypes = append(a.InstanceTypes, inventory.Count{Value: fmt.Sprintf("t%02d", i), N: 12 - i})
	}
	r := Build(t0, Selection{PendingFor: "5m"}, []Result{
		{Context: "a", Cluster: a}, {Context: "b", Cluster: b}, {Context: "down", Cause: "timed out"},
	})
	s := r.Summary
	if s.Clusters != 3 || s.Reachable != 2 || s.NodesTotal != 6 || s.NodesReady != 6 || s.PodsRunning != 20 {
		t.Errorf("summary totals %+v", s)
	}
	if len(s.InstanceTypes) != 11 || s.InstanceTypes[10] != (inventory.Count{Value: "other", N: 3 + 3}) {
		t.Errorf("instance types should be top 10 plus other: %+v", s.InstanceTypes)
	}
	if want := []Provisioner{{Kind: "karpenter", Name: "general", Nodes: 6}}; !reflect.DeepEqual(s.Provisioners, want) {
		t.Errorf("provisioners %+v", s.Provisioners)
	}
	if want := []inventory.Count{{Value: "v1.29.8", N: 1}, {Value: "v1.30.4", N: 1}}; !reflect.DeepEqual(s.ControlPlanes, want) {
		t.Errorf("control planes %+v", s.ControlPlanes)
	}
}

func TestKubeletRange(t *testing.T) {
	tests := []struct {
		in   []inventory.Count
		want string
	}{
		{nil, "-"},
		{[]inventory.Count{{Value: "v1.30.4-eks-x", N: 2}}, "1.30.4"},
		{[]inventory.Count{{Value: "v1.30.4", N: 2}, {Value: "v1.29.8", N: 1}, {Value: "v1.30.0", N: 1}}, "1.29.8-1.30.4"},
	}
	for _, tt := range tests {
		if got := KubeletRange(tt.in); got != tt.want {
			t.Errorf("KubeletRange(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
