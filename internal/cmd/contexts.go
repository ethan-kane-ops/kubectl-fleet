package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
)

func newContextsCmd(kubeFlags *genericclioptions.ConfigFlags) *cobra.Command {
	var (
		o       fleetOptions
		probe   bool
		timeout time.Duration
	)
	c := &cobra.Command{
		Use:     "contexts",
		Short:   "List kubeconfig contexts, with optional reachability probe",
		Long:    contextsLong,
		Example: contextsExample,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := o.format()
			if err != nil {
				return err
			}
			cfg, err := kubeconfig.Load(kubeFlags)
			if err != nil {
				return err
			}
			sel, err := o.selectContexts(cmd.Context(), kubeFlags, cfg, true)
			if err != nil {
				return err
			}
			// contexts is the reachability view: a context --where could not
			// read is shown as an unreachable row, not dropped.
			refs, err := withFailedRefs(kubeFlags, cfg, &o, sel)
			if err != nil {
				return err
			}
			tbl := &output.Table{
				Headers:     []string{"CONTEXT", "CLUSTER", "NAMESPACE", "AUTH"},
				WideHeaders: []string{"REACHABLE", "VERSION", "LATENCY", "ERROR"},
				NoHeaders:   o.noHeaders,
			}

			var probes []fleet.ClusterResult[reach]
			switch {
			case sel.probes != nil:
				// --where already probed every context; reuse it rather than
				// waiting on dead clusters a second time.
				probe = true
				for _, r := range refs {
					p := sel.probes[r.Name]
					probes = append(probes, fleet.ClusterResult[reach]{Context: r.Name, Value: reach{version: p.version, latency: p.latency}, Err: p.err})
				}
			case probe:
				probes = fleet.Run(cmd.Context(), refs, o.parallelism, func(_ context.Context, r kubeconfig.ContextRef) (reach, error) {
					return probeOne(kubeconfig.ForContext(kubeFlags, cfg, r.Name), timeout)
				})
			}
			for i, r := range refs {
				wide := []string{"", "", "", ""}
				if probe && i < len(probes) {
					p := probes[i]
					switch {
					case p.Err != nil:
						wide = []string{"no", "", p.Value.latency.String(), p.Err.Error()}
					default:
						wide = []string{"yes", p.Value.version, p.Value.latency.String(), ""}
					}
				}
				tbl.Append([]string{r.Name, r.Cluster, r.Namespace, r.AuthInfo}, wide)
			}
			if probe && f == output.FormatTable {
				f = output.FormatWide
			}
			if err := output.Print(cmd.OutOrStdout(), tbl, f); err != nil {
				return err
			}
			if o.strict && probe {
				if n := fleet.CountErrors(probes); n > 0 {
					return fmt.Errorf("%d of %d contexts unreachable", n, len(probes))
				}
			}
			return nil
		},
	}
	c.Flags().BoolVar(&probe, "check", false, "probe each context's /version endpoint")
	c.Flags().DurationVar(&timeout, "timeout", 5*time.Second, "per-context probe timeout")
	o.addFlags(c, defaultParallelism)
	c.Flags().Lookup("parallelism").Usage = "max contexts probed at once (0=unbounded)"
	c.Flags().Lookup("strict").Usage = "exit non-zero if any probed context is unreachable (requires --check)"
	c.Flags().StringVar(&o.contexts, "filter", "", "regex selecting kubeconfig contexts by name")
	_ = c.Flags().MarkDeprecated("filter", "use --contexts")
	return c
}

// withFailedRefs returns the selected contexts plus those --where could not
// read, in name order.
func withFailedRefs(kubeFlags *genericclioptions.ConfigFlags, cfg *clientcmdapi.Config, o *fleetOptions, sel selection) ([]kubeconfig.ContextRef, error) {
	if len(sel.failures) == 0 {
		return sel.refs, nil
	}
	keep := make(map[string]bool, len(sel.refs)+len(sel.failures))
	for _, r := range sel.refs {
		keep[r.Name] = true
	}
	for _, f := range sel.failures {
		keep[f.Context] = true
	}
	all, err := kubeconfig.Select(kubeFlags, cfg, o.contexts, o.exclude)
	if err != nil {
		return nil, err
	}
	var out []kubeconfig.ContextRef
	for _, r := range all {
		if keep[r.Name] {
			out = append(out, r)
		}
	}
	return out, nil
}

type reach struct {
	version string
	latency time.Duration
}

// probeOne times a /version call against one context. The timeout is applied
// per request, so contexts queued behind --parallelism get the full budget.
func probeOne(kc *kubeconfig.Context, timeout time.Duration) (reach, error) {
	restCfg, err := kc.ToRESTConfig()
	if err != nil {
		return reach{}, err
	}
	restCfg.Timeout = timeout
	disco, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		return reach{}, fmt.Errorf("discovery client: %w", err)
	}
	start := time.Now()
	v, err := disco.ServerVersion()
	lat := time.Since(start)
	if err != nil {
		return reach{latency: lat}, err
	}
	return reach{version: v.GitVersion, latency: lat}, nil
}
