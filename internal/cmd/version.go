package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/buildinfo"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
)

func newVersionCmd(kubeFlags *genericclioptions.ConfigFlags) *cobra.Command {
	var (
		o          fleetOptions
		clientOnly bool
	)
	c := &cobra.Command{
		Use:   "version",
		Short: "Print plugin and per-cluster Kubernetes server versions",
		RunE: func(cmd *cobra.Command, args []string) error {
			f, err := o.format()
			if err != nil {
				return err
			}
			tbl := &output.Table{
				Headers:     []string{"CONTEXT", "COMPONENT", "VERSION"},
				WideHeaders: []string{"GIT_COMMIT", "BUILD_DATE", "PLATFORM", "ERROR"},
				NoHeaders:   o.noHeaders,
			}
			tbl.Append(clientRow())

			if clientOnly {
				return output.Print(cmd.OutOrStdout(), tbl, f)
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

			results := fleet.Run(cmd.Context(), refs, o.parallelism,
				func(ctx context.Context, r kubeconfig.ContextRef) (*version.Info, error) {
					return probeServerVersion(kubeconfig.ForContext(kubeFlags, cfg, r.Name))
				})

			for _, res := range results {
				if res.Err != nil {
					if o.errors {
						tbl.Append(
							[]string{res.Context, "k8s-server", "<error>"},
							[]string{"", "", "", res.Err.Error()},
						)
					}
					continue
				}
				v := res.Value
				tbl.Append(
					[]string{res.Context, "k8s-server", v.GitVersion},
					[]string{v.GitCommit, v.BuildDate, v.Platform, ""},
				)
			}
			if o.errors {
				promoteErrorColumn(tbl)
			}
			if err := output.Print(cmd.OutOrStdout(), tbl, f); err != nil {
				return err
			}
			return o.finish(cmd.ErrOrStderr(), sel, fleet.Failures(results), len(refs))
		},
	}
	c.Flags().BoolVar(&clientOnly, "client", false, "show client version only, skip cluster probes")
	o.addFlags(c, defaultParallelism)
	o.addErrorsFlag(c)
	c.Flags().Lookup("strict").Usage = "exit non-zero if any context failed (ignored with --client)"
	return c
}

func clientRow() ([]string, []string) {
	return []string{"(client)", "kubectl-fleet", buildinfo.Version},
		[]string{buildinfo.Commit, buildinfo.Date, "", ""}
}

func probeServerVersion(kc *kubeconfig.Context) (*version.Info, error) {
	disco, err := kc.ToDiscoveryClient()
	if err != nil {
		return nil, fmt.Errorf("discovery client: %w", err)
	}
	v, err := disco.ServerVersion()
	if err != nil {
		return nil, fmt.Errorf("server version: %w", err)
	}
	return v, nil
}
