package cmd

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/metadata"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/where"
)

// fleetOptions holds the flags every fleet command shares, so selection,
// parallelism and output mean the same thing on each of them.
type fleetOptions struct {
	contexts    string
	exclude     string
	parallelism int
	strict      bool
	output      string
	noHeaders   bool
	where       []string
}

func (o *fleetOptions) addFlags(c *cobra.Command, defaultParallelism int) {
	f := c.Flags()
	f.StringVar(&o.contexts, "contexts", "", "regex selecting kubeconfig contexts by name (default: all)")
	f.StringVar(&o.exclude, "exclude", "", "regex of context names to leave out, applied after --contexts")
	f.IntVar(&o.parallelism, "parallelism", defaultParallelism, "max contexts queried at once (0=unbounded)")
	f.BoolVar(&o.strict, "strict", false, "exit non-zero if any context failed")
	f.StringVarP(&o.output, "output", "o", "table", "output format: table|wide|json|yaml|name")
	f.BoolVar(&o.noHeaders, "no-headers", false, "suppress header row in table/wide output")
	f.StringArrayVar(&o.where, "where", nil, "filter contexts by live attributes; repeatable, all must match: version<1.30, nodes>=50, reachable, !reachable")
}

func (o *fleetOptions) format() (output.Format, error) {
	return output.ParseFormat(o.output)
}

// probeTimeout bounds each --where probe request when --request-timeout is
// not set, so one dead cluster cannot stall the selection pass.
const probeTimeout = 10 * time.Second

var nodesGVR = schema.GroupVersionResource{Version: "v1", Resource: "nodes"}

// probeResult is what the --where pass learned about one context.
type probeResult struct {
	version string
	latency time.Duration
	err     error
}

// selection is the outcome of picking contexts: the ones to run against,
// the ones whose --where attributes could not be read, and how many
// unreachable ones --where reachable skipped.
type selection struct {
	refs               []kubeconfig.ContextRef
	failures           []fleet.Failure
	skippedUnreachable int
	// probes holds the --where probe per context name; nil without --where.
	probes map[string]probeResult
}

// selectContexts applies --context, --contexts, --exclude and --where.
// allowUnreachable is true only for `contexts`, the one command that can
// show contexts it cannot reach.
func (o *fleetOptions) selectContexts(ctx context.Context, kubeFlags *genericclioptions.ConfigFlags, cfg *clientcmdapi.Config, allowUnreachable bool) (selection, error) {
	filter, err := where.Parse(o.where)
	if err != nil {
		return selection{}, err
	}
	if filter.Reachability() == where.UnreachableOnly && !allowUnreachable {
		return selection{}, fmt.Errorf("--where '!reachable' selects contexts this command cannot query; list them with: kubectl fleet contexts --where '!reachable'")
	}
	refs, err := kubeconfig.Select(kubeFlags, cfg, o.contexts, o.exclude)
	if err != nil {
		return selection{}, err
	}
	if len(refs) == 0 {
		return selection{}, fmt.Errorf("no matching contexts")
	}
	if filter.Empty() {
		return selection{refs: refs}, nil
	}

	type probed struct {
		probeResult
		attrs where.Attributes
	}
	results := fleet.Run(ctx, refs, o.parallelism, func(ctx context.Context, r kubeconfig.ContextRef) (probed, error) {
		p, attrs := probeContext(ctx, kubeconfig.ForContext(kubeFlags, cfg, r.Name), filter.NeedsNodes())
		return probed{probeResult: p, attrs: attrs}, nil
	})
	sel := selection{probes: make(map[string]probeResult, len(refs))}
	for i, res := range results {
		ref, p := refs[i], res.Value
		sel.probes[ref.Name] = p.probeResult
		unreachable := p.version == "" && p.err != nil
		switch {
		case filter.Reachability() == where.UnreachableOnly:
			if unreachable {
				sel.refs = append(sel.refs, ref)
			}
		case unreachable && filter.Reachability() == where.ReachOnly:
			sel.skippedUnreachable++
		case p.err != nil:
			sel.failures = append(sel.failures, fleet.Failure{Context: ref.Name, Err: p.err})
		case filter.Match(p.attrs):
			sel.refs = append(sel.refs, ref)
		}
	}
	if len(sel.refs) == 0 && len(sel.failures) == 0 {
		return selection{}, fmt.Errorf("no matching contexts: --where excluded all %d", len(refs))
	}
	return sel, nil
}

// probeContext reads a context's version and, when needed, its node count
// (a metadata-only list, so no Node bodies are transferred).
func probeContext(ctx context.Context, kc *kubeconfig.Context, needNodes bool) (probeResult, where.Attributes) {
	restCfg, err := kc.ToRESTConfig()
	if err != nil {
		return probeResult{err: err}, where.Attributes{}
	}
	if restCfg.Timeout == 0 {
		restCfg.Timeout = probeTimeout
	}
	disco, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		return probeResult{err: fmt.Errorf("discovery client: %w", err)}, where.Attributes{}
	}
	start := time.Now()
	v, err := disco.ServerVersion()
	res := probeResult{latency: time.Since(start)}
	if err != nil {
		res.err = fmt.Errorf("server version: %w", err)
		return res, where.Attributes{}
	}
	res.version = v.GitVersion
	attrs := where.Attributes{Version: v.GitVersion}
	if needNodes {
		n, err := countNodes(ctx, restCfg)
		if err != nil {
			res.err = fmt.Errorf("count nodes: %w", err)
			return res, attrs
		}
		attrs.Nodes = n
	}
	return res, attrs
}

func countNodes(ctx context.Context, restCfg *rest.Config) (int, error) {
	mc, err := metadata.NewForConfig(restCfg)
	if err != nil {
		return 0, err
	}
	n, opts := 0, metav1.ListOptions{Limit: 500}
	for {
		list, err := mc.Resource(nodesGVR).List(ctx, opts)
		if err != nil {
			return 0, err
		}
		n += len(list.Items)
		if list.Continue == "" {
			return n, nil
		}
		opts.Continue = list.Continue
	}
}

// report prints each context --where could not read, and how many
// unreachable contexts it skipped.
func (s selection) report(w io.Writer) {
	for _, f := range s.failures {
		_, _ = fmt.Fprintf(w, "warn: %s: %v\n", f.Context, f.Err)
	}
	reportSkipped(w, s.skippedUnreachable)
}

// reportSkipped notes contexts that --where reachable left out.
func reportSkipped(w io.Writer, n int) {
	if n > 0 {
		_, _ = fmt.Fprintf(w, "warn: skipped %d unreachable\n", n)
	}
}
