package cmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/health"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
)

func newStatusCmd(kubeFlags *genericclioptions.ConfigFlags) *cobra.Command {
	var (
		filter      string
		parallelism int
		outputFlag  string
	)
	c := &cobra.Command{
		Use:   "status",
		Short: "Per-cluster health summary across kubeconfig contexts",
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
			if len(refs) == 0 {
				return fmt.Errorf("no matching contexts")
			}
			results := fleet.Run(cmd.Context(), refs, parallelism,
				func(ctx context.Context, r kubeconfig.ContextRef) (health.Summary, error) {
					return statusOne(ctx, kubeFlags, r)
				})
			tbl := &output.Table{
				Headers:     []string{"CONTEXT", "VERSION", "NODES", "PODS", "PENDING", "CRASHLOOP"},
				WideHeaders: []string{"FAILED", "TOTAL_PODS", "TOP_NOISY", "ERROR"},
			}
			for _, res := range results {
				if res.Err != nil {
					tbl.Append(
						[]string{res.Context, "?", "?", "?", "?", "?"},
						[]string{"?", "?", "", res.Err.Error()},
					)
					continue
				}
				s := res.Value
				tbl.Append(
					[]string{
						res.Context,
						s.ServerVersion,
						fmt.Sprintf("%d/%d", s.NodesReady, s.NodesTotal),
						fmt.Sprintf("%d", s.PodsRunning),
						fmt.Sprintf("%d", s.PodsPending),
						fmt.Sprintf("%d", s.PodsCrashLoop),
					},
					[]string{
						fmt.Sprintf("%d", s.PodsFailed),
						fmt.Sprintf("%d", s.PodsTotal),
						formatNoisy(s.TopNoisyNS),
						"",
					},
				)
			}
			return output.Print(cmd.OutOrStdout(), tbl, f)
		},
	}
	c.Flags().StringVar(&filter, "contexts", "", "regex applied to context names")
	c.Flags().IntVar(&parallelism, "parallelism", 8, "max parallel cluster calls (0=unbounded)")
	c.Flags().StringVarP(&outputFlag, "output", "o", "table", "output format: table|wide|json|yaml")
	return c
}

func statusOne(ctx context.Context, kubeFlags *genericclioptions.ConfigFlags, r kubeconfig.ContextRef) (health.Summary, error) {
	restCfg, err := kubeconfig.RESTConfigFor(kubeFlags, r.Name)
	if err != nil {
		return health.Summary{}, err
	}
	clients, err := k8s.New(restCfg)
	if err != nil {
		return health.Summary{}, err
	}
	return health.Summarize(ctx, clients.Typed, clients.Discovery)
}

func formatNoisy(ns []health.NamespaceNoise) string {
	if len(ns) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ns))
	for _, n := range ns {
		parts = append(parts, fmt.Sprintf("%s(%d)", n.Namespace, n.NonRunning))
	}
	return strings.Join(parts, ",")
}
