// Package report builds the fleet report behind `kubectl fleet report`: one
// collection pass per cluster, attention items, a fleet rollup and its
// renderings.
package report

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/pager"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/health"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/inventory"
)

// Node labels the report reads.
const (
	labelZone         = "topology.kubernetes.io/zone"
	labelInstanceType = "node.kubernetes.io/instance-type"
	labelNodePool     = "karpenter.sh/nodepool"
	labelNodeGroup    = "eks.amazonaws.com/nodegroup"
)

// unknown stands in for a missing zone or instance type label.
const unknown = "unknown"

// topNamespaces is how many namespaces an attention item names.
const topNamespaces = 3

// Options tunes Collect.
type Options struct {
	// PendingFor is how long a pod must be Pending to count as stuck.
	PendingFor time.Duration
	// Now is the time pod ages are measured against.
	Now time.Time
}

// Provisioner is how a set of nodes is provisioned: a Karpenter NodePool, an
// EKS node group, or neither.
type Provisioner struct {
	Kind  string `json:"kind"`
	Name  string `json:"name"`
	Nodes int    `json:"nodes"`
}

// Cluster is everything the report knows about one cluster.
type Cluster struct {
	ControlPlane     string                  `json:"controlPlane"`
	NodesReady       int                     `json:"nodesReady"`
	NodesTotal       int                     `json:"nodesTotal"`
	Kubelets         []inventory.Count       `json:"kubelets"`
	Zones            []inventory.Count       `json:"zones"`
	InstanceTypes    []inventory.Count       `json:"instanceTypes"`
	Provisioners     []Provisioner           `json:"provisioners"`
	PodsRunning      int                     `json:"podsRunning"`
	PodsPending      int                     `json:"podsPending"`
	PodsStuckPending int                     `json:"podsStuckPending"`
	PodsCrashLoop    int                     `json:"podsCrashLoop"`
	PodsFailed       int                     `json:"podsFailed"`
	PodsTotal        int                     `json:"podsTotal"`
	CrashLoopNS      []health.NamespaceNoise `json:"crashLoopNamespaces"`
	StuckPendingNS   []health.NamespaceNoise `json:"stuckPendingNamespaces"`
	Components       []inventory.Component   `json:"components"`
	NodesForbidden   bool                    `json:"nodesForbidden,omitempty"`
	PodsForbidden    bool                    `json:"podsForbidden,omitempty"`
	Partial          []string                `json:"partial,omitempty"`
}

// Collect reads one cluster: /version, one paged node list, one paged pod
// list and the kube-system workloads. A forbidden node or pod list is
// recorded on the Cluster, not returned as an error, so the report can say
// the cluster could not be inspected.
func Collect(ctx context.Context, cs kubernetes.Interface, disco discovery.DiscoveryInterface, opts Options) (Cluster, error) {
	var c Cluster
	v, err := disco.ServerVersion()
	if err != nil {
		return c, fmt.Errorf("server version: %w", err)
	}
	c.ControlPlane = v.GitVersion

	forbidden, err := collectNodes(ctx, cs, &c)
	switch {
	case forbidden:
		c.NodesForbidden = true
	case err != nil:
		return c, fmt.Errorf("list nodes: %w", err)
	}
	forbidden, err = collectPods(ctx, cs, &c, opts)
	switch {
	case forbidden:
		c.PodsForbidden = true
	case err != nil:
		return c, fmt.Errorf("list pods: %w", err)
	}

	comps, partial, err := inventory.SystemComponents(ctx, cs)
	if err != nil {
		return c, err
	}
	c.Components, c.Partial = comps, partial
	return c, nil
}

func collectNodes(ctx context.Context, cs kubernetes.Interface, c *Cluster) (forbidden bool, err error) {
	kubelets, zones, types := inventory.Counter{}, inventory.Counter{}, inventory.Counter{}
	provs := map[Provisioner]int{}
	lister := pager.New(func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
		return cs.CoreV1().Nodes().List(ctx, o)
	})
	err = lister.EachListItem(ctx, metav1.ListOptions{}, func(obj runtime.Object) error {
		n, ok := obj.(*corev1.Node)
		if !ok {
			return fmt.Errorf("unexpected list item %T", obj)
		}
		c.NodesTotal++
		if health.IsNodeReady(n) {
			c.NodesReady++
		}
		kubelets.Add(n.Status.NodeInfo.KubeletVersion)
		zones.Add(labelOr(n, labelZone))
		types.Add(labelOr(n, labelInstanceType))
		provs[provisionerOf(n)]++
		return nil
	})
	if err != nil {
		return apierrors.IsForbidden(err), err
	}
	c.Kubelets, c.Zones, c.InstanceTypes = kubelets.Sorted(), zones.Sorted(), types.Sorted()
	c.Provisioners = sortedProvisioners(provs)
	return false, nil
}

func collectPods(ctx context.Context, cs kubernetes.Interface, c *Cluster, opts Options) (forbidden bool, err error) {
	crashNS, stuckNS := map[string]int{}, map[string]int{}
	lister := pager.New(func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
		return cs.CoreV1().Pods("").List(ctx, o)
	})
	err = lister.EachListItem(ctx, metav1.ListOptions{}, func(obj runtime.Object) error {
		p, ok := obj.(*corev1.Pod)
		if !ok {
			return fmt.Errorf("unexpected list item %T", obj)
		}
		c.PodsTotal++
		switch p.Status.Phase {
		case corev1.PodRunning:
			c.PodsRunning++
		case corev1.PodPending:
			c.PodsPending++
			// A creation time in the future (clock skew) gives a negative
			// age, which is never stuck.
			if opts.Now.Sub(p.CreationTimestamp.Time) >= opts.PendingFor {
				c.PodsStuckPending++
				stuckNS[p.Namespace]++
			}
		case corev1.PodFailed:
			c.PodsFailed++
		}
		if health.IsCrashLoop(p) {
			c.PodsCrashLoop++
			crashNS[p.Namespace]++
		}
		return nil
	})
	if err != nil {
		return apierrors.IsForbidden(err), err
	}
	c.CrashLoopNS = health.TopNoisy(crashNS, topNamespaces)
	c.StuckPendingNS = health.TopNoisy(stuckNS, topNamespaces)
	return false, nil
}

func labelOr(n *corev1.Node, key string) string {
	if v := n.Labels[key]; v != "" {
		return v
	}
	return unknown
}

func provisionerOf(n *corev1.Node) Provisioner {
	if v := n.Labels[labelNodePool]; v != "" {
		return Provisioner{Kind: "karpenter", Name: v}
	}
	if v := n.Labels[labelNodeGroup]; v != "" {
		return Provisioner{Kind: "nodegroup", Name: v}
	}
	return Provisioner{Kind: "other"}
}

// sortedProvisioners orders by node count, most first, then kind and name.
func sortedProvisioners(m map[Provisioner]int) []Provisioner {
	out := make([]Provisioner, 0, len(m))
	for p, n := range m {
		p.Nodes = n
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Nodes != b.Nodes {
			return a.Nodes > b.Nodes
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Name < b.Name
	})
	return out
}
