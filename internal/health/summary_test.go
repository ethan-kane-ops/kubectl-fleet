package health

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
)

func node(name string, ready bool) *corev1.Node {
	cond := corev1.NodeCondition{Type: corev1.NodeReady, Status: corev1.ConditionFalse}
	if ready {
		cond.Status = corev1.ConditionTrue
	}
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status:     corev1.NodeStatus{Conditions: []corev1.NodeCondition{cond}},
	}
}

func pod(ns, name string, phase corev1.PodPhase, crash bool) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status:     corev1.PodStatus{Phase: phase},
	}
	if crash {
		p.Status.ContainerStatuses = []corev1.ContainerStatus{
			{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}},
		}
	}
	return p
}

func TestSummarize(t *testing.T) {
	cs := fake.NewSimpleClientset(
		node("n1", true), node("n2", true), node("n3", false),
		pod("default", "a", corev1.PodRunning, false),
		pod("default", "b", corev1.PodRunning, false),
		pod("kube-system", "c", corev1.PodPending, false),
		pod("kube-system", "d", corev1.PodPending, false),
		pod("kube-system", "e", corev1.PodPending, false),
		pod("noisy", "f", corev1.PodRunning, true),
		pod("noisy", "g", corev1.PodFailed, false),
	)
	s, err := Summarize(context.Background(), cs, nil, fakePods{cs}, Options{})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.NodesReady != 2 || s.NodesTotal != 3 {
		t.Errorf("nodes ready=%d total=%d want 2/3", s.NodesReady, s.NodesTotal)
	}
	if s.PodsRunning != 3 || s.PodsPending != 3 || s.PodsFailed != 1 {
		t.Errorf("pods running=%d pending=%d failed=%d", s.PodsRunning, s.PodsPending, s.PodsFailed)
	}
	if s.PodsCrashLoop != 1 {
		t.Errorf("crashloop=%d want 1", s.PodsCrashLoop)
	}
	if s.PodsTotal != 7 {
		t.Errorf("total=%d want 7", s.PodsTotal)
	}
	if len(s.TopNoisyNS) == 0 || s.TopNoisyNS[0].Namespace != "kube-system" || s.TopNoisyNS[0].Unhealthy != 3 {
		t.Errorf("top noisy: %+v want kube-system=3 first", s.TopNoisyNS)
	}
}

func TestSummarizeEmptyCluster(t *testing.T) {
	cs := fake.NewSimpleClientset()
	s, err := Summarize(context.Background(), cs, nil, fakePods{cs}, Options{})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.NodesTotal != 0 || s.PodsTotal != 0 {
		t.Errorf("expected zeroes, got %+v", s)
	}
}

func podWithRestart(ns, name string, terminatedAt time.Time) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:         "c",
				RestartCount: 1,
				LastTerminationState: corev1.ContainerState{
					Terminated: &corev1.ContainerStateTerminated{
						FinishedAt: metav1.NewTime(terminatedAt),
						Reason:     "Error",
					},
				},
			}},
		},
	}
}

func TestSummarize_ContainersRestartedInWindow(t *testing.T) {
	now := time.Now()
	cs := fake.NewSimpleClientset(
		podWithRestart("default", "recent-1", now.Add(-2*time.Minute)),
		podWithRestart("default", "recent-2", now.Add(-4*time.Minute)),
		podWithRestart("default", "old-1", now.Add(-20*time.Minute)),
		pod("default", "no-restart", corev1.PodRunning, false),
	)
	s, err := Summarize(context.Background(), cs, nil, fakePods{cs}, Options{Since: 5 * time.Minute})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.ContainersRestartedInWindow != 2 {
		t.Errorf("ContainersRestartedInWindow = %d want 2", s.ContainersRestartedInWindow)
	}
}

func TestSummarize_WindowDisabled(t *testing.T) {
	cs := fake.NewSimpleClientset(
		podWithRestart("default", "recent", time.Now().Add(-1*time.Minute)),
	)
	s, err := Summarize(context.Background(), cs, nil, fakePods{cs}, Options{})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.ContainersRestartedInWindow != 0 {
		t.Errorf("ContainersRestartedInWindow = %d want 0 (window disabled)", s.ContainersRestartedInWindow)
	}
}

func TestSummarizeNoisyNamespaces(t *testing.T) {
	cases := []struct {
		name string
		pods []*corev1.Pod
		want []NamespaceNoise
	}{
		{
			name: "succeeded pods are not noise",
			pods: []*corev1.Pod{
				pod("jobs", "a", corev1.PodSucceeded, false),
				pod("jobs", "b", corev1.PodSucceeded, false),
				pod("web", "c", corev1.PodPending, false),
			},
			want: []NamespaceNoise{{"web", 1}},
		},
		{
			name: "pending crashlooping pod counts once",
			pods: []*corev1.Pod{pod("init", "a", corev1.PodPending, true)},
			want: []NamespaceNoise{{"init", 1}},
		},
		{
			// Unknown pods are read with Succeeded ones, as a count only.
			name: "unknown phase counts in the total only",
			pods: []*corev1.Pod{pod("lost", "a", corev1.PodUnknown, false)},
			want: []NamespaceNoise{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := fake.NewSimpleClientset()
			for _, p := range tc.pods {
				if err := cs.Tracker().Add(p); err != nil {
					t.Fatalf("add pod: %v", err)
				}
			}
			s, err := Summarize(context.Background(), cs, nil, fakePods{cs}, Options{})
			if err != nil {
				t.Fatalf("Summarize: %v", err)
			}
			if !reflect.DeepEqual(s.TopNoisyNS, tc.want) {
				t.Errorf("top noisy=%+v want %+v", s.TopNoisyNS, tc.want)
			}
		})
	}
}

type failingVersion struct{ discovery.DiscoveryInterface }

func (failingVersion) ServerVersion() (*version.Info, error) {
	return nil, errors.New("connection refused")
}

func TestSummarizeFailsWhenVersionFails(t *testing.T) {
	cs := fake.NewSimpleClientset(node("n1", true))
	_, err := Summarize(context.Background(), cs, failingVersion{cs.Discovery()}, fakePods{cs}, Options{})
	if err == nil || !strings.Contains(err.Error(), "server version: connection refused") {
		t.Fatalf("want server version error, got %v", err)
	}
	for _, a := range cs.Actions() {
		if a.GetVerb() == "list" {
			t.Errorf("listed %s after version failed", a.GetResource().Resource)
		}
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
