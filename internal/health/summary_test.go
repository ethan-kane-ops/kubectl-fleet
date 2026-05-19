package health

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
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
	s, err := Summarize(context.Background(), cs, nil, Options{})
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
	if len(s.TopNoisyNS) == 0 || s.TopNoisyNS[0].Namespace != "kube-system" || s.TopNoisyNS[0].NonRunning != 3 {
		t.Errorf("top noisy: %+v want kube-system=3 first", s.TopNoisyNS)
	}
}

func TestSummarizeEmptyCluster(t *testing.T) {
	cs := fake.NewSimpleClientset()
	s, err := Summarize(context.Background(), cs, nil, Options{})
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

func TestSummarize_PodsRestartedInWindow(t *testing.T) {
	now := time.Now()
	cs := fake.NewSimpleClientset(
		podWithRestart("default", "recent-1", now.Add(-2*time.Minute)),
		podWithRestart("default", "recent-2", now.Add(-4*time.Minute)),
		podWithRestart("default", "old-1", now.Add(-20*time.Minute)),
		pod("default", "no-restart", corev1.PodRunning, false),
	)
	s, err := Summarize(context.Background(), cs, nil, Options{Since: 5 * time.Minute})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.PodsRestartedInWindow != 2 {
		t.Errorf("PodsRestartedInWindow = %d want 2", s.PodsRestartedInWindow)
	}
}

func TestSummarize_WindowDisabled(t *testing.T) {
	cs := fake.NewSimpleClientset(
		podWithRestart("default", "recent", time.Now().Add(-1*time.Minute)),
	)
	s, err := Summarize(context.Background(), cs, nil, Options{})
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if s.PodsRestartedInWindow != 0 {
		t.Errorf("PodsRestartedInWindow = %d want 0 (window disabled)", s.PodsRestartedInWindow)
	}
}
