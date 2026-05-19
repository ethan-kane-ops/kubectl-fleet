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
		filter      string
		probe       bool
		timeout     time.Duration
		parallelism int
		outputFlag  string
		noHeaders   bool
	)
	c := &cobra.Command{
		Use:   "contexts",
		Short: "List kubeconfig contexts, with optional reachability probe",
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := output.ParseFormat(outputFlag)
			if err != nil {
				return err
			}
			cfg, err := kubeconfig.Load(kubeFlags)
			if err != nil {
				return err
			}
			refs, err := kubeconfig.Contexts(cfg, filter)
			if err != nil {
				return err
			}
			tbl := &output.Table{
				Headers:     []string{"CONTEXT", "CLUSTER", "NAMESPACE", "AUTH"},
				WideHeaders: []string{"REACHABLE", "VERSION", "LATENCY", "ERROR"},
				NoHeaders:   noHeaders,
			}

			var probes []fleet.ClusterResult[reach]
			if probe {
				ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
				defer cancel()
				probes = fleet.Run(ctx, refs, parallelism, func(ctx context.Context, r kubeconfig.ContextRef) (reach, error) {
					return probeOne(ctx, kubeFlags, r)
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
			return output.Print(cmd.OutOrStdout(), tbl, f)
		},
	}
	c.Flags().StringVar(&filter, "filter", "", "regex applied to context names")
	c.Flags().BoolVar(&probe, "check", false, "probe each context's /version endpoint")
	c.Flags().DurationVar(&timeout, "timeout", 5*time.Second, "per-context probe timeout")
	c.Flags().IntVar(&parallelism, "parallelism", 8, "max parallel probes (0=unbounded)")
	c.Flags().StringVarP(&outputFlag, "output", "o", "table", "output format: table|wide|json|yaml|name")
	c.Flags().BoolVar(&noHeaders, "no-headers", false, "suppress header row in table/wide output")
	return c
}

type reach struct {
	version string
	latency time.Duration
}

func probeOne(ctx context.Context, kubeFlags *genericclioptions.ConfigFlags, r kubeconfig.ContextRef) (reach, error) {
	restCfg, err := kubeconfig.RESTConfigFor(kubeFlags, r.Name)
	if err != nil {
		return reach{}, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		restCfg.Timeout = time.Until(deadline)
	}
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

