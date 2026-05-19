package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

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
		since       time.Duration
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
			opts := health.Options{Since: since}
			results := fleet.Run(cmd.Context(), refs, parallelism,
				func(ctx context.Context, r kubeconfig.ContextRef) (health.Summary, error) {
					return statusOne(ctx, kubeFlags, r, opts)
				})

			headers := []string{"CONTEXT", "VERSION", "NODES", "PODS", "PENDING", "CRASHLOOP"}
			if since > 0 {
				headers = append(headers, "RESTARTS_"+since.String())
			}
			wideHeaders := []string{"FAILED", "TOTAL_PODS", "TOP_NOISY", "ERROR"}
			tbl := &output.Table{Headers: headers, WideHeaders: wideHeaders}

			for _, res := range results {
				if res.Err != nil {
					row := []string{res.Context, "?", "?", "?", "?", "?"}
					if since > 0 {
						row = append(row, "?")
					}
					tbl.Append(row, []string{"?", "?", "", res.Err.Error()})
					continue
				}
				s := res.Value
				row := []string{
					res.Context,
					s.ServerVersion,
					fmt.Sprintf("%d/%d", s.NodesReady, s.NodesTotal),
					fmt.Sprintf("%d", s.PodsRunning),
					fmt.Sprintf("%d", s.PodsPending),
					fmt.Sprintf("%d", s.PodsCrashLoop),
				}
				if since > 0 {
					row = append(row, fmt.Sprintf("%d", s.PodsRestartedInWindow))
				}
				tbl.Append(row, []string{
					fmt.Sprintf("%d", s.PodsFailed),
					fmt.Sprintf("%d", s.PodsTotal),
					formatNoisy(s.TopNoisyNS),
					"",
				})
			}
			return output.Print(cmd.OutOrStdout(), tbl, f)
		},
	}
	c.Flags().StringVar(&filter, "contexts", "", "regex applied to context names")
	c.Flags().IntVar(&parallelism, "parallelism", 8, "max parallel cluster calls (0=unbounded)")
	c.Flags().StringVarP(&outputFlag, "output", "o", "table", "output format: table|wide|json|yaml")
	c.Flags().DurationVar(&since, "since", 0, "if set, add RESTARTS_<dur> column counting containers with LastTermination.FinishedAt within window (e.g. 5m, 1h)")
	return c
}

func statusOne(ctx context.Context, kubeFlags *genericclioptions.ConfigFlags, r kubeconfig.ContextRef, opts health.Options) (health.Summary, error) {
	restCfg, err := kubeconfig.RESTConfigFor(kubeFlags, r.Name)
	if err != nil {
		return health.Summary{}, err
	}
	clients, err := k8s.New(restCfg)
	if err != nil {
		return health.Summary{}, err
	}
	return health.Summarize(ctx, clients.Typed, clients.Discovery, opts)
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
