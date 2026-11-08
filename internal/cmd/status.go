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
		noHeaders   bool
		strict      bool
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
			refs, err := kubeconfig.Select(kubeFlags, cfg, filter)
			if err != nil {
				return err
			}
			if len(refs) == 0 {
				return fmt.Errorf("no matching contexts")
			}
			opts := health.Options{Since: since}
			results := fleet.Run(cmd.Context(), refs, parallelism,
				func(ctx context.Context, r kubeconfig.ContextRef) (health.Summary, error) {
					return statusOne(ctx, kubeconfig.ForContext(kubeFlags, cfg, r.Name), opts)
				})

			headers := []string{"CONTEXT", "VERSION", "NODES", "PODS", "PENDING", "CRASHLOOP"}
			if since > 0 {
				headers = append(headers, "RESTARTS_"+shortDuration(since))
			}
			wideHeaders := []string{"FAILED", "TOTAL_PODS", "TOP_NOISY", "ERROR"}
			tbl := &output.Table{Headers: headers, WideHeaders: wideHeaders, NoHeaders: noHeaders}

			records := make([]statusRecord, 0, len(results))
			for _, res := range results {
				if res.Err != nil {
					records = append(records, statusRecord{Context: res.Context, Error: res.Err.Error()})
					row := []string{res.Context, "?", "?", "?", "?", "?"}
					if since > 0 {
						row = append(row, "?")
					}
					tbl.Append(row, []string{"?", "?", "", res.Err.Error()})
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warn: %s: %v\n", res.Context, res.Err)
					continue
				}
				s := res.Value
				records = append(records, statusRecord{Context: res.Context, Summary: &s})
				row := []string{
					res.Context,
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
			if f.Structured() {
				err = output.Encode(cmd.OutOrStdout(), records, f)
			} else {
				err = output.Print(cmd.OutOrStdout(), tbl, f)
			}
			if err != nil {
				return err
			}
			if strict {
				if n := fleet.CountErrors(results); n > 0 {
					return fmt.Errorf("%d of %d contexts failed", n, len(results))
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&filter, "contexts", "", "regex applied to context names")
	c.Flags().IntVar(&parallelism, "parallelism", 8, "max parallel cluster calls (0=unbounded)")
	c.Flags().StringVarP(&outputFlag, "output", "o", "table", "output format: table|wide|json|yaml|name")
	c.Flags().DurationVar(&since, "since", 0, "if set, add RESTARTS_<dur> column counting containers with LastTermination.FinishedAt within window (e.g. 5m, 1h)")
	c.Flags().BoolVar(&noHeaders, "no-headers", false, "suppress header row in table/wide output")
	c.Flags().BoolVar(&strict, "strict", false, "exit non-zero if any context failed")
	return c
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
