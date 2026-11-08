package cmd

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/health"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/where"
)

func newStatusCmd(kubeFlags *genericclioptions.ConfigFlags) *cobra.Command {
	var (
		o            fleetOptions
		since        time.Duration
		problemsOnly bool
		sortBy       string
		top          int
	)
	c := &cobra.Command{
		Use:   "status",
		Short: "Per-cluster health summary across kubeconfig contexts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := o.format()
			if err != nil {
				return err
			}
			if !slices.Contains(statusSortKeys, sortBy) && sortBy != "" {
				return fmt.Errorf("--sort-by must be one of: %s", strings.Join(statusSortKeys, ", "))
			}
			if top < 0 {
				return fmt.Errorf("--top must be 0 or more")
			}
			cfg, err := kubeconfig.Load(kubeFlags)
			if err != nil {
				return err
			}
			sel, err := o.selectContexts(cmd.Context(), kubeFlags, cfg, false)
			if err != nil {
				return err
			}
			refs := sel.refs
			opts := health.Options{Since: since}
			results := fleet.Run(cmd.Context(), refs, o.parallelism,
				func(ctx context.Context, r kubeconfig.ContextRef) (health.Summary, error) {
					return statusOne(ctx, kubeconfig.ForContext(kubeFlags, cfg, r.Name), opts)
				})

			headers := []string{"CONTEXT", "VERSION", "NODES", "PODS", "PENDING", "CRASHLOOP"}
			if since > 0 {
				headers = append(headers, "RESTARTS_"+shortDuration(since))
			}
			wideHeaders := []string{"FAILED", "TOTAL_PODS", "TOP_NOISY", "ERROR"}
			tbl := &output.Table{Headers: headers, WideHeaders: wideHeaders, NoHeaders: o.noHeaders}

			// Healthy clusters are filtered, sorted and trimmed; failed ones
			// are reported separately (summary, or rows with --errors) and
			// always kept as JSON error records after them.
			var ok []contextSummary
			failures := append([]fleet.Failure(nil), sel.failures...)
			for _, res := range results {
				if res.Err != nil {
					failures = append(failures, fleet.Failure{Context: res.Context, Err: res.Err})
					continue
				}
				if !problemsOnly || problemCluster(res.Value) {
					ok = append(ok, contextSummary{context: res.Context, summary: res.Value})
				}
			}
			sortSummaries(ok, sortBy)
			if top > 0 && len(ok) > top {
				ok = ok[:top]
			}
			sort.Slice(failures, func(i, j int) bool { return failures[i].Context < failures[j].Context })
			entries := make([]statusEntry, 0, len(ok)+len(failures))
			for _, cs := range ok {
				entries = append(entries, statusEntry{context: cs.context, summary: cs.summary})
			}
			for _, fl := range failures {
				entries = append(entries, statusEntry{context: fl.Context, err: fl.Err})
			}
			// By name (the default), failed contexts sit in name order among
			// the rest, as before; by a size or version key they follow.
			if sortBy == "" || sortBy == "context" {
				sort.SliceStable(entries, func(i, j int) bool { return entries[i].context < entries[j].context })
			}

			records := make([]statusRecord, 0, len(entries))
			for _, e := range entries {
				if e.err != nil {
					records = append(records, statusRecord{Context: e.context, Error: e.err.Error()})
					if o.errors {
						row := []string{e.context, "?", "?", "?", "?", "?"}
						if since > 0 {
							row = append(row, "?")
						}
						tbl.Append(row, []string{"?", "?", "", e.err.Error()})
					}
					continue
				}
				s := e.summary
				records = append(records, statusRecord{Context: e.context, Summary: &s})
				row := []string{
					e.context,
					s.ServerVersion,
					fmt.Sprintf("%d/%d", s.NodesReady, s.NodesTotal),
					fmt.Sprintf("%d", s.PodsRunning),
					fmt.Sprintf("%d", s.PodsPending),
					fmt.Sprintf("%d", s.PodsCrashLoop),
				}
				if since > 0 {
					row = append(row, fmt.Sprintf("%d", s.ContainersRestartedInWindow))
				}
				tbl.Append(row, []string{
					fmt.Sprintf("%d", s.PodsFailed),
					fmt.Sprintf("%d", s.PodsTotal),
					formatNoisy(s.TopNoisyNS),
					"",
				})
			}
			if o.errors {
				promoteErrorColumn(tbl)
			}
			if f.Structured() {
				err = output.Encode(cmd.OutOrStdout(), records, f)
			} else {
				err = output.Print(cmd.OutOrStdout(), tbl, f)
			}
			if err != nil {
				return err
			}
			return o.finish(cmd.ErrOrStderr(), sel, fleet.Failures(results), len(refs))
		},
	}
	c.Flags().BoolVar(&problemsOnly, "problems", false, "show only clusters with NotReady nodes or Pending, Failed or crashlooping pods")
	c.Flags().StringVar(&sortBy, "sort-by", "", "order clusters: nodes, pods or version (largest first) or context")
	c.Flags().IntVar(&top, "top", 0, "keep only the first N clusters after sorting (0 = all)")
	c.Flags().DurationVar(&since, "since", 0, "if set, add RESTARTS_<dur> column counting containers with LastTermination.FinishedAt within window (e.g. 5m, 1h)")
	o.addFlags(c, defaultParallelism)
	o.addErrorsFlag(c)
	return c
}

// statusSortKeys are the values --sort-by accepts.
var statusSortKeys = []string{"nodes", "pods", "version", "context"}

// statusEntry is one row of status output: a summary, or a failure.
type statusEntry struct {
	context string
	summary health.Summary
	err     error
}

type contextSummary struct {
	context string
	summary health.Summary
}

// problemCluster reports a cluster with NotReady nodes or Pending, Failed or
// crashlooping pods.
func problemCluster(s health.Summary) bool {
	return s.NodesReady < s.NodesTotal || s.PodsPending+s.PodsFailed+s.PodsCrashLoop > 0
}

// sortSummaries orders clusters by key: nodes, pods and version largest
// first, context (and the default, "") by name. Ties fall back to name.
func sortSummaries(rows []contextSummary, key string) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i].summary, rows[j].summary
		c := 0
		switch key {
		case "nodes":
			c = b.NodesTotal - a.NodesTotal
		case "pods":
			c = b.PodsTotal - a.PodsTotal
		case "version":
			c = where.CompareServerVersions(b.ServerVersion, a.ServerVersion)
		}
		if c != 0 {
			return c < 0
		}
		return rows[i].context < rows[j].context
	})
}

// statusRecord is one context's entry in `status -o json|yaml`. A failed
// context carries only Context and Error.
type statusRecord struct {
	Context string `json:"context"`
	*health.Summary
	Error string `json:"error,omitempty"`
}

func statusOne(ctx context.Context, kc *kubeconfig.Context, opts health.Options) (health.Summary, error) {
	clients, err := k8s.New(kc)
	if err != nil {
		return health.Summary{}, err
	}
	return health.Summarize(ctx, clients.Typed, clients.Discovery, opts)
}

// shortDuration drops zero trailing units: 1h0m0s -> 1h, 5m0s -> 5m.
func shortDuration(d time.Duration) string {
	s := d.String()
	if strings.HasSuffix(s, "m0s") {
		s = strings.TrimSuffix(s, "0s")
	}
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

func formatNoisy(ns []health.NamespaceNoise) string {
	if len(ns) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, fmt.Sprintf("%s(%d)", n.Namespace, n.Unhealthy))
	}
	return strings.Join(parts, ",")
}
