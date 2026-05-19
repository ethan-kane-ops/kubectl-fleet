// Package health summarises per-cluster readiness for `kubectl fleet status`.
package health

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
)

// Options tunes Summarize.
type Options struct {
	// Since enables a count of container restart events whose
	// LastTerminationState.Terminated.FinishedAt falls within the window.
	// Zero disables the window count (PodsRestartedInWindow stays 0).
	Since time.Duration
}

// Summary is the per-cluster snapshot.
type Summary struct {
	ServerVersion         string
	NodesReady            int
	NodesTotal            int
	PodsRunning           int
	PodsPending           int
	PodsCrashLoop         int
	PodsFailed            int
	PodsTotal             int
	PodsRestartedInWindow int
	TopNoisyNS            []NamespaceNoise
}

// NamespaceNoise reports a single namespace's non-Running pod count.
type NamespaceNoise struct {
	Namespace  string
	NonRunning int
}

// Summarize gathers the nodes/pods/version snapshot for one cluster.
//
// disco may be nil; when set, used for server version (slightly cheaper than
// a typed client call via Discovery()).
func Summarize(ctx context.Context, cs kubernetes.Interface, disco discovery.DiscoveryInterface, opts Options) (Summary, error) {
	var s Summary

	if disco == nil {
		disco = cs.Discovery()
	}
	if v, err := disco.ServerVersion(); err == nil && v != nil {
		s.ServerVersion = v.GitVersion
	}

	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return s, fmt.Errorf("list nodes: %w", err)
	}
	s.NodesTotal = len(nodes.Items)
	for i := range nodes.Items {
		if isNodeReady(&nodes.Items[i]) {
			s.NodesReady++
		}
	}

	pods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return s, fmt.Errorf("list pods: %w", err)
	}
	s.PodsTotal = len(pods.Items)
	noisy := map[string]int{}
	var cutoff time.Time
	if opts.Since > 0 {
		cutoff = time.Now().Add(-opts.Since)
	}
	for i := range pods.Items {
		p := &pods.Items[i]
		switch p.Status.Phase {
		case corev1.PodRunning:
			s.PodsRunning++
		case corev1.PodPending:
			s.PodsPending++
			noisy[p.Namespace]++
		case corev1.PodFailed:
			s.PodsFailed++
			noisy[p.Namespace]++
		default:
			noisy[p.Namespace]++
		}
		if isCrashLoop(p) {
			s.PodsCrashLoop++
			noisy[p.Namespace]++
		}
		if !cutoff.IsZero() {
			s.PodsRestartedInWindow += countRestartsInWindow(p, cutoff)
		}
	}
	s.TopNoisyNS = topNoisy(noisy, 3)
	return s, nil
}

func isNodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func isCrashLoop(p *corev1.Pod) bool {
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
			return true
		}
	}
	return false
}

// countRestartsInWindow returns the number of containers in p whose last
// termination happened after cutoff. A container restarting many times in the
// window still counts once because only the most recent termination carries
// a timestamp — accept the floor; the metric is a triage signal, not a SLA.
func countRestartsInWindow(p *corev1.Pod, cutoff time.Time) int {
	n := 0
	for _, cs := range p.Status.ContainerStatuses {
		if cs.LastTerminationState.Terminated == nil {
			continue
		}
		if cs.LastTerminationState.Terminated.FinishedAt.After(cutoff) {
			n++
		}
	}
	for _, cs := range p.Status.InitContainerStatuses {
		if cs.LastTerminationState.Terminated == nil {
			continue
		}
		if cs.LastTerminationState.Terminated.FinishedAt.After(cutoff) {
			n++
		}
	}
	return n
}

func topNoisy(m map[string]int, n int) []NamespaceNoise {
	out := make([]NamespaceNoise, 0, len(m))
	for k, v := range m {
		out = append(out, NamespaceNoise{Namespace: k, NonRunning: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NonRunning != out[j].NonRunning {
			return out[i].NonRunning > out[j].NonRunning
		}
		return out[i].Namespace < out[j].Namespace
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
