// Package inventory collects the versions running in a cluster, for
// `kubectl fleet version --detailed`: control plane, kubelets, node OS,
// kernel, container runtime, architecture and kube-system components.
package inventory

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/pager"
)

// Count is one distinct value and how many nodes report it.
type Count struct {
	Value string `json:"value"`
	N     int    `json:"nodes"`
}

// Component is a kube-system Deployment or DaemonSet and the version (image
// tag) of its first container.
type Component struct {
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Version string `json:"version"`
}

// Cluster is one cluster's version inventory. Partial lists what could not
// be read for lack of RBAC (e.g. "nodes: forbidden").
type Cluster struct {
	ControlPlane string      `json:"controlPlane"`
	Kubelets     []Count     `json:"kubelets"`
	OSImages     []Count     `json:"osImages"`
	Kernels      []Count     `json:"kernels"`
	Runtimes     []Count     `json:"containerRuntimes"`
	Arches       []Count     `json:"architectures"`
	Components   []Component `json:"components"`
	Partial      []string    `json:"partial,omitempty"`
}

// systemNamespace is where core system components run.
const systemNamespace = "kube-system"

// Collect reads one cluster's inventory: /version, a paged node list, and
// kube-system Deployments and DaemonSets. A forbidden list is recorded in
// Partial; any other error fails the cluster.
func Collect(ctx context.Context, cs kubernetes.Interface, disco discovery.DiscoveryInterface) (Cluster, error) {
	var c Cluster
	v, err := disco.ServerVersion()
	if err != nil {
		return c, fmt.Errorf("server version: %w", err)
	}
	c.ControlPlane = v.GitVersion

	kubelets, oses, kernels, runtimes, arches := counter{}, counter{}, counter{}, counter{}, counter{}
	nodes := pager.New(func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
		return cs.CoreV1().Nodes().List(ctx, o)
	})
	err = nodes.EachListItem(ctx, metav1.ListOptions{}, func(obj runtime.Object) error {
		n, ok := obj.(*corev1.Node)
		if !ok {
			return fmt.Errorf("unexpected list item %T", obj)
		}
		info := n.Status.NodeInfo
		kubelets.add(info.KubeletVersion)
		oses.add(info.OSImage)
		kernels.add(info.KernelVersion)
		runtimes.add(info.ContainerRuntimeVersion)
		arches.add(info.Architecture)
		return nil
	})
	if err := partial(&c, "nodes", err); err != nil {
		return c, err
	}
	c.Kubelets, c.OSImages, c.Kernels, c.Runtimes, c.Arches = kubelets.sorted(), oses.sorted(), kernels.sorted(), runtimes.sorted(), arches.sorted()

	deploys, err := cs.AppsV1().Deployments(systemNamespace).List(ctx, metav1.ListOptions{})
	if err := partial(&c, "kube-system deployments", err); err != nil {
		return c, err
	}
	if deploys != nil {
		for _, d := range deploys.Items {
			c.Components = append(c.Components, component(d.Name, "Deployment", d.Spec.Template.Spec))
		}
	}
	daemons, err := cs.AppsV1().DaemonSets(systemNamespace).List(ctx, metav1.ListOptions{})
	if err := partial(&c, "kube-system daemonsets", err); err != nil {
		return c, err
	}
	if daemons != nil {
		for _, d := range daemons.Items {
			c.Components = append(c.Components, component(d.Name, "DaemonSet", d.Spec.Template.Spec))
		}
	}
	sort.Slice(c.Components, func(i, j int) bool { return c.Components[i].Name < c.Components[j].Name })
	return c, nil
}

// partial records a forbidden list as a partial result and returns any
// other error.
func partial(c *Cluster, what string, err error) error {
	switch {
	case err == nil:
		return nil
	case apierrors.IsForbidden(err):
		c.Partial = append(c.Partial, what+": forbidden")
		return nil
	}
	return fmt.Errorf("list %s: %w", what, err)
}

func component(name, kind string, spec corev1.PodSpec) Component {
	version := ""
	if len(spec.Containers) > 0 {
		version = imageVersion(spec.Containers[0].Image)
	}
	return Component{Name: name, Kind: kind, Version: version}
}

// imageVersion is an image reference's tag; without a tag, the first 12
// hex digits of its digest; without either, "latest".
func imageVersion(image string) string {
	ref, digest, hasDigest := strings.Cut(image, "@")
	name := ref[strings.LastIndex(ref, "/")+1:]
	if _, tag, ok := strings.Cut(name, ":"); ok {
		return tag
	}
	if hasDigest {
		algo, hex, _ := strings.Cut(digest, ":")
		return algo + ":" + hex[:min(len(hex), 12)]
	}
	return "latest"
}

type counter map[string]int

func (c counter) add(v string) {
	if v != "" {
		c[v]++
	}
}

// sorted orders values by node count, most first, then by value.
func (c counter) sorted() []Count {
	out := make([]Count, 0, len(c))
	for v, n := range c {
		out = append(out, Count{Value: v, N: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Value < out[j].Value
	})
	return out
}

// SpreadRow is one component version and how many clusters run it.
type SpreadRow struct {
	Component string
	Version   string
	Clusters  int
}

// Spread summarises a fleet: per component, each version and the number of
// clusters running it (a cluster with mixed kubelets counts for each).
// Platform rows come first in a fixed order (kube-apiserver, kubelet,
// os-image, kernel, container-runtime, arch), then kube-system components
// by name; versions by cluster count, most first, then by version.
func Spread(clusters map[string]Cluster) []SpreadRow {
	platform := []string{"kube-apiserver", "kubelet", "os-image", "kernel", "container-runtime", "arch"}
	counts := map[string]map[string]int{}
	add := func(comp, version string) {
		if version == "" {
			return
		}
		if counts[comp] == nil {
			counts[comp] = map[string]int{}
		}
		counts[comp][version]++
	}
	for _, c := range clusters {
		add("kube-apiserver", c.ControlPlane)
		for comp, list := range map[string][]Count{"kubelet": c.Kubelets, "os-image": c.OSImages, "kernel": c.Kernels, "container-runtime": c.Runtimes, "arch": c.Arches} {
			for _, v := range list {
				add(comp, v.Value)
			}
		}
		for _, comp := range c.Components {
			add(comp.Name, comp.Version)
		}
	}
	order := append([]string(nil), platform...)
	var comps []string
	for comp := range counts {
		if !slices.Contains(platform, comp) {
			comps = append(comps, comp)
		}
	}
	sort.Strings(comps)
	order = append(order, comps...)

	var rows []SpreadRow
	for _, comp := range order {
		var vs []SpreadRow
		for v, n := range counts[comp] {
			vs = append(vs, SpreadRow{Component: comp, Version: v, Clusters: n})
		}
		sort.Slice(vs, func(i, j int) bool {
			if vs[i].Clusters != vs[j].Clusters {
				return vs[i].Clusters > vs[j].Clusters
			}
			return vs[i].Version < vs[j].Version
		})
		rows = append(rows, vs...)
	}
	return rows
}
