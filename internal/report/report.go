package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/health"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/inventory"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/where"
)

// Kind is the sort of problem an attention item reports, in severity order.
type Kind string

const (
	KindFailed       Kind = "failed"
	KindUninspected  Kind = "uninspected"
	KindNotReady     Kind = "nodesNotReady"
	KindCrashLoop    Kind = "crashLoop"
	KindStuckPending Kind = "stuckPending"
	KindVersion      Kind = "version"
	// KindPartial is informational and never makes a report need attention.
	KindPartial Kind = "partial"
)

var kindOrder = map[Kind]int{KindFailed: 0, KindUninspected: 1, KindNotReady: 2, KindCrashLoop: 3, KindStuckPending: 4, KindVersion: 5, KindPartial: 6}

// Item is one thing in the report that needs attention.
type Item struct {
	Context string `json:"context"`
	Kind    Kind   `json:"kind"`
	Message string `json:"message"`
	Count   int    `json:"count,omitempty"`
}

// Counts reports whether the item makes the report need attention.
func (i Item) Counts() bool { return i.Kind != KindPartial }

// Result is one context's outcome: its Cluster, or the cause of its failure.
type Result struct {
	Context string
	Cluster *Cluster
	Cause   string
}

// Selection records how the report's contexts were chosen.
type Selection struct {
	Context       string   `json:"context,omitempty"`
	Contexts      []string `json:"contexts,omitempty"`
	Exclude       string   `json:"exclude,omitempty"`
	Where         []string `json:"where,omitempty"`
	ExpectVersion string   `json:"expectVersion,omitempty"`
	PendingFor    string   `json:"pendingFor"`
}

// Summary is the fleet rollup.
type Summary struct {
	Clusters         int               `json:"clusters"`
	Reachable        int               `json:"reachable"`
	NodesReady       int               `json:"nodesReady"`
	NodesTotal       int               `json:"nodesTotal"`
	PodsRunning      int               `json:"podsRunning"`
	PodsPending      int               `json:"podsPending"`
	PodsStuckPending int               `json:"podsStuckPending"`
	PodsCrashLoop    int               `json:"podsCrashLoop"`
	PodsFailed       int               `json:"podsFailed"`
	PodsTotal        int               `json:"podsTotal"`
	ControlPlanes    []inventory.Count `json:"controlPlanes"`
	Kubelets         []inventory.Count `json:"kubelets"`
	Zones            []inventory.Count `json:"zones"`
	InstanceTypes    []inventory.Count `json:"instanceTypes"`
	Provisioners     []Provisioner     `json:"provisioners"`
}

// ClusterRecord is one cluster in the report: its data, or its failure
// cause.
type ClusterRecord struct {
	Context string `json:"context"`
	*Cluster
	Error string `json:"error,omitempty"`
}

// Report is the whole fleet report.
type Report struct {
	GeneratedAt time.Time             `json:"generatedAt"`
	Selection   Selection             `json:"selection"`
	Summary     Summary               `json:"summary"`
	Attention   []Item                `json:"attention"`
	Clusters    []ClusterRecord       `json:"clusters"`
	Components  []inventory.SpreadRow `json:"components"`
}

// NeedsAttention reports whether any item counts.
func (r Report) NeedsAttention() bool {
	for _, i := range r.Attention {
		if i.Counts() {
			return true
		}
	}
	return false
}

// maxInstanceTypes is how many instance types the rollup names before
// summing the rest as "other".
const maxInstanceTypes = 10

// Build turns per-context results into a report.
func Build(now time.Time, sel Selection, results []Result) Report {
	r := Report{GeneratedAt: now, Selection: sel, Attention: []Item{}, Components: []inventory.SpreadRow{}}
	sorted := append([]Result(nil), results...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Context < sorted[j].Context })

	planes, kubelets, zones, types := inventory.Counter{}, inventory.Counter{}, inventory.Counter{}, inventory.Counter{}
	provs := map[Provisioner]int{}
	comps := map[string]inventory.Cluster{}
	s := &r.Summary
	for _, res := range sorted {
		s.Clusters++
		r.Attention = append(r.Attention, attention(res, sel)...)
		if res.Cluster == nil {
			r.Clusters = append(r.Clusters, ClusterRecord{Context: res.Context, Error: res.Cause})
			continue
		}
		c := res.Cluster
		r.Clusters = append(r.Clusters, ClusterRecord{Context: res.Context, Cluster: c})
		s.Reachable++
		s.NodesReady += c.NodesReady
		s.NodesTotal += c.NodesTotal
		s.PodsRunning += c.PodsRunning
		s.PodsPending += c.PodsPending
		s.PodsStuckPending += c.PodsStuckPending
		s.PodsCrashLoop += c.PodsCrashLoop
		s.PodsFailed += c.PodsFailed
		s.PodsTotal += c.PodsTotal
		planes.Add(c.ControlPlane)
		addCounts(kubelets, c.Kubelets)
		addCounts(zones, c.Zones)
		addCounts(types, c.InstanceTypes)
		for _, p := range c.Provisioners {
			n := p.Nodes
			p.Nodes = 0
			provs[p] += n
		}
		comps[res.Context] = inventory.Cluster{Components: c.Components}
	}
	s.ControlPlanes, s.Kubelets, s.Zones = planes.Sorted(), kubelets.Sorted(), zones.Sorted()
	s.InstanceTypes = topWithOther(types.Sorted(), maxInstanceTypes)
	s.Provisioners = sortedProvisioners(provs)
	if spread := inventory.Spread(comps); spread != nil {
		r.Components = spread
	}
	sort.SliceStable(r.Attention, func(i, j int) bool {
		a, b := r.Attention[i], r.Attention[j]
		if a.Kind != b.Kind {
			return kindOrder[a.Kind] < kindOrder[b.Kind]
		}
		return a.Context < b.Context
	})
	return r
}

func addCounts(c inventory.Counter, counts []inventory.Count) {
	for _, v := range counts {
		c[v.Value] += v.N
	}
}

func topWithOther(counts []inventory.Count, n int) []inventory.Count {
	if len(counts) <= n {
		return counts
	}
	other := 0
	for _, c := range counts[n:] {
		other += c.N
	}
	return append(counts[:n:n], inventory.Count{Value: "other", N: other})
}

// attention applies the rules to one context. Rules that need a list the
// cluster refused are skipped, never treated as passing: the refusal is its
// own item.
func attention(res Result, sel Selection) []Item {
	if res.Cluster == nil {
		return []Item{{Context: res.Context, Kind: KindFailed, Message: "context failed: " + res.Cause}}
	}
	c := res.Cluster
	var items []Item
	add := func(kind Kind, count int, format string, args ...any) {
		items = append(items, Item{Context: res.Context, Kind: kind, Message: fmt.Sprintf(format, args...), Count: count})
	}
	if c.NodesForbidden {
		add(KindUninspected, 0, "could not inspect nodes: forbidden")
	}
	if c.PodsForbidden {
		add(KindUninspected, 0, "could not inspect pods: forbidden")
	}
	if !c.NodesForbidden && c.NodesReady < c.NodesTotal {
		n := c.NodesTotal - c.NodesReady
		add(KindNotReady, n, "%d of %d nodes NotReady", n, c.NodesTotal)
	}
	if !c.PodsForbidden && c.PodsCrashLoop > 0 {
		add(KindCrashLoop, c.PodsCrashLoop, "%d crashlooping pods (%s)", c.PodsCrashLoop, namespaces(c.CrashLoopNS))
	}
	if !c.PodsForbidden && c.PodsStuckPending > 0 {
		add(KindStuckPending, c.PodsStuckPending, "%d pods pending longer than %s (%s)", c.PodsStuckPending, sel.PendingFor, namespaces(c.StuckPendingNS))
	}
	if want := sel.ExpectVersion; want != "" {
		if !where.MatchVersion(c.ControlPlane, want) {
			add(KindVersion, 0, "control plane %s is not %s", c.ControlPlane, want)
		}
		if !c.NodesForbidden {
			var off []string
			n := 0
			for _, k := range c.Kubelets {
				if !where.MatchVersion(k.Value, want) {
					off = append(off, fmt.Sprintf("%s(%d)", k.Value, k.N))
					n += k.N
				}
			}
			if n > 0 {
				add(KindVersion, n, "kubelets not on %s: %s", want, strings.Join(off, ", "))
			}
		}
	}
	if len(c.Partial) > 0 {
		add(KindPartial, 0, "partial data: %s", strings.Join(c.Partial, ", "))
	}
	return items
}

func namespaces(ns []health.NamespaceNoise) string {
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, fmt.Sprintf("%s(%d)", n.Namespace, n.Unhealthy))
	}
	return strings.Join(parts, ", ")
}

// KubeletRange is the lowest and highest kubelet version as "1.29.8-1.30.4",
// one version when all agree, or "-" when there are none.
func KubeletRange(kubelets []inventory.Count) string {
	if len(kubelets) == 0 {
		return "-"
	}
	lo, hi := kubelets[0].Value, kubelets[0].Value
	for _, k := range kubelets[1:] {
		if where.CompareServerVersions(k.Value, lo) < 0 {
			lo = k.Value
		}
		if where.CompareServerVersions(k.Value, hi) > 0 {
			hi = k.Value
		}
	}
	if where.CompareServerVersions(lo, hi) == 0 {
		return where.ShortVersion(lo)
	}
	return where.ShortVersion(lo) + "-" + where.ShortVersion(hi)
}
