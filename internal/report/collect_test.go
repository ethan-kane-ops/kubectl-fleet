package report

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/health"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/inventory"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
)

var t0 = time.Date(2026, 11, 14, 10, 30, 0, 0, time.UTC)

func node(name string, ready bool, kubelet string, labels map[string]string) *corev1.Node {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name, Labels: labels},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: status}},
			NodeInfo:   corev1.NodeSystemInfo{KubeletVersion: kubelet},
		},
	}
}

func pod(ns, name string, phase corev1.PodPhase, created time.Time) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, CreationTimestamp: metav1.NewTime(created)},
		Status:     corev1.PodStatus{Phase: phase},
	}
}

func collect(t *testing.T, objs ...runtime.Object) (Cluster, error) {
	t.Helper()
	cs := fake.NewSimpleClientset(objs...)
	return Collect(context.Background(), cs, cs.Discovery(), fakePods{cs}, Options{PendingFor: 5 * time.Minute, Now: t0})
}

func TestCollectNodes(t *testing.T) {
	karp := map[string]string{"karpenter.sh/nodepool": "general", "topology.kubernetes.io/zone": "eu-west-1a", "node.kubernetes.io/instance-type": "m7g.large"}
	c, err := collect(t,
		node("k1", true, "v1.30.4", karp),
		node("k2", true, "v1.30.4", karp),
		node("ng", true, "v1.30.4", map[string]string{"eks.amazonaws.com/nodegroup": "system", "topology.kubernetes.io/zone": "eu-west-1b"}),
		node("bare", false, "v1.29.8", nil),
	)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if c.NodesReady != 3 || c.NodesTotal != 4 {
		t.Errorf("nodes %d/%d, want 3/4", c.NodesReady, c.NodesTotal)
	}
	wantZones := []inventory.Count{{Value: "eu-west-1a", N: 2}, {Value: "eu-west-1b", N: 1}, {Value: "unknown", N: 1}}
	if !reflect.DeepEqual(c.Zones, wantZones) {
		t.Errorf("zones %+v, want %+v", c.Zones, wantZones)
	}
	wantProv := []Provisioner{{Kind: "karpenter", Name: "general", Nodes: 2}, {Kind: "nodegroup", Name: "system", Nodes: 1}, {Kind: "other", Nodes: 1}}
	if !reflect.DeepEqual(c.Provisioners, wantProv) {
		t.Errorf("provisioners %+v, want %+v", c.Provisioners, wantProv)
	}
	wantTypes := []inventory.Count{{Value: "m7g.large", N: 2}, {Value: "unknown", N: 2}}
	if !reflect.DeepEqual(c.InstanceTypes, wantTypes) {
		t.Errorf("instance types %+v, want %+v", c.InstanceTypes, wantTypes)
	}
	wantKubelets := []inventory.Count{{Value: "v1.30.4", N: 3}, {Value: "v1.29.8", N: 1}}
	if !reflect.DeepEqual(c.Kubelets, wantKubelets) {
		t.Errorf("kubelets %+v, want %+v", c.Kubelets, wantKubelets)
	}
}

func TestCollectPods(t *testing.T) {
	crash := pod("b", "crash", corev1.PodRunning, t0.Add(-time.Hour))
	crash.Status.ContainerStatuses = []corev1.ContainerStatus{{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}}
	c, err := collect(t,
		pod("a", "run", corev1.PodRunning, t0.Add(-time.Hour)),
		pod("a", "stuck", corev1.PodPending, t0.Add(-10*time.Minute)),
		pod("a", "new", corev1.PodPending, t0.Add(-time.Minute)),
		pod("a", "future", corev1.PodPending, t0.Add(time.Hour)),
		crash,
		pod("c", "failed", corev1.PodFailed, t0.Add(-time.Hour)),
		pod("c", "done", corev1.PodSucceeded, t0.Add(-time.Hour)),
	)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if c.PodsPending != 3 || c.PodsStuckPending != 1 || c.PodsCrashLoop != 1 || c.PodsFailed != 1 || c.PodsTotal != 7 || c.PodsRunning != 2 {
		t.Errorf("pods %+v", c)
	}
	if want := []health.NamespaceNoise{{Namespace: "a", Unhealthy: 1}}; !reflect.DeepEqual(c.StuckPendingNS, want) {
		t.Errorf("stuck pending ns %+v, want %+v", c.StuckPendingNS, want)
	}
}

func failList(resource string, err error) k8stesting.ReactionFunc {
	return func(a k8stesting.Action) (bool, runtime.Object, error) {
		return a.GetResource().Resource == resource, nil, err
	}
}

func TestCollectForbidden(t *testing.T) {
	for _, res := range []string{"pods", "nodes"} {
		t.Run(res, func(t *testing.T) {
			cs := fake.NewSimpleClientset()
			cs.PrependReactor("list", res, failList(res, apierrors.NewForbidden(schema.GroupResource{Resource: res}, "", nil)))
			c, err := Collect(context.Background(), cs, cs.Discovery(), fakePods{cs}, Options{PendingFor: time.Minute, Now: t0})
			if err != nil {
				t.Fatalf("forbidden %s should not fail the cluster: %v", res, err)
			}
			if res == "pods" && !c.PodsForbidden || res == "nodes" && !c.NodesForbidden {
				t.Errorf("forbidden flag not set: %+v", c)
			}
		})
	}
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("list", "pods", failList("pods", apierrors.NewInternalError(context.DeadlineExceeded)))
	if _, err := Collect(context.Background(), cs, cs.Discovery(), fakePods{cs}, Options{PendingFor: time.Minute, Now: t0}); err == nil || !strings.Contains(err.Error(), "pods:") {
		t.Errorf("want a pod list error, got %v", err)
	}
}

func TestCollectJSONListsAreNeverNull(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("list", "nodes", failList("nodes", apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "", nil)))
	c, err := Collect(context.Background(), cs, cs.Discovery(), fakePods{cs}, Options{PendingFor: time.Minute, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(c)
	if strings.Contains(string(b), "null") {
		t.Errorf("want [] not null in the json contract: %s", b)
	}
}

// fakePods serves the fake clientset's pods as a pods.Source, filtered by
// phase; it has no tables, so pods.Count reads every list in full.
type fakePods struct{ cs kubernetes.Interface }

func (f fakePods) Rows(context.Context, string, func(string, string) error) error {
	return k8s.ErrNoTable
}

func (f fakePods) Pods(ctx context.Context, selector string, fn func(*corev1.Pod) error) error {
	list, err := f.cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for i := range list.Items {
		p := &list.Items[i]
		if phaseMatches(selector, string(p.Status.Phase)) {
			if err := fn(p); err != nil {
				return err
			}
		}
	}
	return nil
}

// phaseMatches applies a status.phase field selector (= or != terms).
func phaseMatches(selector, phase string) bool {
	for _, term := range strings.Split(selector, ",") {
		if v, ok := strings.CutPrefix(term, "status.phase!="); ok && phase == v {
			return false
		}
		if v, ok := strings.CutPrefix(term, "status.phase="); ok && phase != v {
			return false
		}
	}
	return true
}
