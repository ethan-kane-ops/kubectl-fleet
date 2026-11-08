package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"

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
		Use:   "contexts",
		Short: "List kubeconfig contexts, with optional reachability probe",
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := o.format()
			if err != nil {
				return err
			}
			cfg, err := kubeconfig.Load(kubeFlags)
			if err != nil {
				return err
			}
			refs, err := o.selectContexts(kubeFlags, cfg)
			if err != nil {
				return err
			}
			tbl := &output.Table{
				Headers:     []string{"CONTEXT", "CLUSTER", "NAMESPACE", "AUTH"},
				WideHeaders: []string{"REACHABLE", "VERSION", "LATENCY", "ERROR"},
				NoHeaders:   o.noHeaders,
			}

			var probes []fleet.ClusterResult[reach]
			if probe {
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
