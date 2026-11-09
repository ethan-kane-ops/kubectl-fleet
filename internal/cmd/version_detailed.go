package cmd

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/inventory"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
)

// inventoryRecord is one context's entry in `version --detailed -o
// json|yaml`. A failed context carries only Context and Error.
type inventoryRecord struct {
	Context string `json:"context"`
	*inventory.Cluster
	Error string `json:"error,omitempty"`
}

// versionDetailed prints the version inventory of the selected contexts:
// by default the fleet-wide spread (component, version, cluster count);
// with perCluster or -o wide one row per context and value; JSON/YAML
// always per cluster.
func versionDetailed(ctx context.Context, out, errOut io.Writer, o *fleetOptions, kubeFlags *genericclioptions.ConfigFlags, cfg *clientcmdapi.Config, sel selection, f output.Format, perCluster bool) error {
	results := fleet.Run(ctx, sel.refs, o.parallelism, func(ctx context.Context, r kubeconfig.ContextRef) (inventory.Cluster, error) {
		clients, err := k8s.New(kubeconfig.ForContext(kubeFlags, cfg, r.Name))
		if err != nil {
			return inventory.Cluster{}, err
		}
		return inventory.Collect(ctx, clients.Typed, clients.Discovery)
	})
	failures := append(append([]fleet.Failure(nil), sel.failures...), fleet.Failures(results)...)
	sort.Slice(failures, func(i, j int) bool { return failures[i].Context < failures[j].Context })

	var err error
	switch {
	case f.Structured():
		records := make([]inventoryRecord, 0, len(results)+len(sel.failures))
		for _, res := range results {
			if res.Err == nil {
				c := res.Value
				records = append(records, inventoryRecord{Context: res.Context, Cluster: &c})
			}
		}
		for _, fl := range failures {
			records = append(records, inventoryRecord{Context: fl.Context, Error: fl.Err.Error()})
		}
		sort.Slice(records, func(i, j int) bool { return records[i].Context < records[j].Context })
		err = output.Encode(out, records, f)
	case perCluster || f == output.FormatWide:
		err = output.Print(out, perClusterTable(results, failures, o), f)
	default:
		clusters := map[string]inventory.Cluster{}
		for _, res := range results {
			if res.Err == nil {
				clusters[res.Context] = res.Value
			}
		}
		tbl := &output.Table{Headers: []string{"COMPONENT", "VERSION", "CLUSTERS"}, NoHeaders: o.noHeaders}
		for _, r := range inventory.Spread(clusters) {
			tbl.Append([]string{r.Component, r.Version, fmt.Sprint(r.Clusters)}, nil)
		}
		err = output.Print(out, tbl, f)
	}
	if err != nil {
		return err
	}
	reportPartial(errOut, results)
	return o.finish(errOut, sel, fleet.Failures(results), len(sel.refs))
}

func perClusterTable(results []fleet.ClusterResult[inventory.Cluster], failures []fleet.Failure, o *fleetOptions) *output.Table {
	tbl := &output.Table{
		Headers:     []string{"CONTEXT", "COMPONENT", "VERSION", "NODES"},
		WideHeaders: []string{"ERROR"},
		NoHeaders:   o.noHeaders,
	}
	for _, res := range results {
		if res.Err != nil {
			continue
		}
		c := res.Value
		row := func(comp, version, nodes string) {
			tbl.Append([]string{res.Context, comp, version, nodes}, []string{""})
		}
		row("kube-apiserver", c.ControlPlane, "")
		for _, p := range []struct {
			name string
			list []inventory.Count
		}{{"kubelet", c.Kubelets}, {"os-image", c.OSImages}, {"kernel", c.Kernels}, {"container-runtime", c.Runtimes}, {"arch", c.Arches}} {
			for _, v := range p.list {
				row(p.name, v.Value, fmt.Sprint(v.N))
			}
		}
		for _, comp := range c.Components {
			row(comp.Name, comp.Version, "")
		}
		for _, p := range c.Partial {
			row("(partial)", p, "")
		}
	}
	if o.errors {
		for _, fl := range failures {
			tbl.Append([]string{fl.Context, "<error>", "", ""}, []string{fl.Err.Error()})
		}
		promoteErrorColumn(tbl)
	}
	return tbl
}

// reportPartial notes clusters whose inventory is incomplete for lack of
// RBAC; what could be read is still shown.
func reportPartial(w io.Writer, results []fleet.ClusterResult[inventory.Cluster]) {
	var names, reasons []string
	seen := map[string]bool{}
	for _, res := range results {
		if res.Err != nil || len(res.Value.Partial) == 0 {
			continue
		}
		names = append(names, res.Context)
		for _, p := range res.Value.Partial {
			if !seen[p] {
				seen[p] = true
				reasons = append(reasons, p)
			}
		}
	}
	if len(names) > 0 {
		_, _ = fmt.Fprintf(w, "warn: %d contexts returned partial results (%s): %s. Use --per-cluster or -o json to see which.\n",
			len(names), strings.Join(reasons, ", "), strings.Join(names[:min(len(names), namesPerCause)], ", "))
	}
}
