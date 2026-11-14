// Package health summarises per-cluster readiness for `kubectl fleet status`.
package health

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/pager"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/pods"
)

// Options tunes Summarize.
type Options struct {
	// Since enables a count of container restart events whose
	// LastTerminationState.Terminated.FinishedAt falls within the window.
	// Zero disables the window count (ContainersRestartedInWindow stays 0).
	Since time.Duration
}

// Summary is the per-cluster snapshot. The JSON tags are the -o json/yaml
// contract of `kubectl fleet status`.
type Summary struct {
	ServerVersion               string           `json:"serverVersion"`
	NodesReady                  int              `json:"nodesReady"`
	NodesTotal                  int              `json:"nodesTotal"`
	PodsRunning                 int              `json:"podsRunning"`
	PodsPending                 int              `json:"podsPending"`
	PodsCrashLoop               int              `json:"podsCrashLoop"`
	PodsFailed                  int              `json:"podsFailed"`
	PodsTotal                   int              `json:"podsTotal"`
	ContainersRestartedInWindow int              `json:"containersRestartedInWindow"`
	TopNoisyNS                  []NamespaceNoise `json:"topNoisyNamespaces"`
}

// NamespaceNoise reports a single namespace's unhealthy pod count: its
// Pending and Failed pods. Crashlooping pods are read as table rows without
// a namespace, so they are not attributed.
type NamespaceNoise struct {
	Namespace string `json:"namespace"`
	Unhealthy int    `json:"unhealthy"`
}

// Summarize gathers the nodes/pods/version snapshot for one cluster.
//
// disco may be nil; when set, used for server version (slightly cheaper than
// a typed client call via Discovery()).
func Summarize(ctx context.Context, cs kubernetes.Interface, disco discovery.DiscoveryInterface, src pods.Source, opts Options) (Summary, error) {
	var s Summary

	if disco == nil {
		disco = cs.Discovery()
	}
	// A cluster that cannot answer /version will not answer the lists either;
	// failing here reports the real error instead of waiting on two more
	// requests to a dead server.
	v, err := disco.ServerVersion()
	if err != nil {
		return s, fmt.Errorf("server version: %w", err)
	}
	s.ServerVersion = v.GitVersion

	// Nodes carry large status.images lists; page them like pods.
	nodeLister := pager.New(func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
		return cs.CoreV1().Nodes().List(ctx, o)
	})
	err = nodeLister.EachListItem(ctx, metav1.ListOptions{}, func(obj runtime.Object) error {
		n, ok := obj.(*corev1.Node)
		if !ok {
			return fmt.Errorf("unexpected list item %T", obj)
		}
		s.NodesTotal++
		if IsNodeReady(n) {
			s.NodesReady++
		}
		return nil
	})
	if err != nil {
		return s, fmt.Errorf("list nodes: %w", err)
	}

	c, err := pods.Count(ctx, src, pods.Options{Since: opts.Since, Now: time.Now()})
	if err != nil {
		return s, err
	}
	s.PodsRunning, s.PodsPending, s.PodsFailed, s.PodsCrashLoop, s.PodsTotal = c.Running, c.Pending, c.Failed, c.CrashLoop, c.Total
	s.ContainersRestartedInWindow = c.RestartedInWindow
	s.TopNoisyNS = TopNoisy(c.NoisyNS, 3)
	return s, nil
}

func IsNodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func TopNoisy(m map[string]int, n int) []NamespaceNoise {
	out := make([]NamespaceNoise, 0, len(m))
	for k, v := range m {
		out = append(out, NamespaceNoise{Namespace: k, Unhealthy: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Unhealthy != out[j].Unhealthy {
			return out[i].Unhealthy > out[j].Unhealthy
		}
		return out[i].Namespace < out[j].Namespace
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}
