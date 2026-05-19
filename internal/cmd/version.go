package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/buildinfo"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
)

func newVersionCmd(kubeFlags *genericclioptions.ConfigFlags) *cobra.Command {
	var (
		clientOnly  bool
		filter      string
		parallelism int
		outputFlag  string
	)
	c := &cobra.Command{
		Use:   "version",
		Short: "Print plugin and per-cluster Kubernetes server versions",
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := output.ParseFormat(outputFlag)
			if err != nil {
				return err
			}
			tbl := &output.Table{
				Headers:     []string{"CONTEXT", "COMPONENT", "VERSION"},
				WideHeaders: []string{"GIT_COMMIT", "BUILD_DATE", "PLATFORM", "ERROR"},
			}
			tbl.Append(clientRow())

			if clientOnly {
				return output.Print(cmd.OutOrStdout(), tbl, f)
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
				func(ctx context.Context, r kubeconfig.ContextRef) (*version.Info, error) {
					return probeServerVersion(kubeFlags, r)
				})

			for _, res := range results {
				if res.Err != nil {
					tbl.Append(
						[]string{res.Context, "k8s-server", "<error>"},
						[]string{"", "", "", res.Err.Error()},
					)
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warn: %s: %v\n", res.Context, res.Err)
					continue
				}
				v := res.Value
				tbl.Append(
					[]string{res.Context, "k8s-server", v.GitVersion},
					[]string{v.GitCommit, v.BuildDate, v.Platform, ""},
				)
			}
			return output.Print(cmd.OutOrStdout(), tbl, f)
		},
	}
	c.Flags().BoolVar(&clientOnly, "client", false, "show client version only, skip cluster probes")
	c.Flags().StringVar(&filter, "contexts", "", "regex applied to context names")
	c.Flags().IntVar(&parallelism, "parallelism", 8, "max parallel cluster calls (0=unbounded)")
	c.Flags().StringVarP(&outputFlag, "output", "o", "table", "output format: table|wide|json|yaml")
	return c
}

func clientRow() ([]string, []string) {
	return []string{"(client)", "kubectl-fleet", buildinfo.Version},
		[]string{buildinfo.Commit, buildinfo.Date, "", ""}
}

func probeServerVersion(kubeFlags *genericclioptions.ConfigFlags, r kubeconfig.ContextRef) (*version.Info, error) {
	restCfg, err := kubeconfig.RESTConfigFor(kubeFlags, r.Name)
	if err != nil {
		return nil, err
	}
	clients, err := k8s.New(restCfg)
	if err != nil {
		return nil, err
	}
	v, err := clients.Discovery.ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("server version: %w", err)
	}
	return v, nil
}
