package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/pods"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/report"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/versions"
)

// reportFormats are the -o values report accepts.
var reportFormats = []string{"text", "markdown", "html", "json", "yaml"}

// skippedCause is the failure cause of a context --skip-unreachable left out.
const skippedCause = "unreachable (skipped by --skip-unreachable)"

func newReportCmd(kubeFlags *genericclioptions.ConfigFlags) *cobra.Command {
	var (
		o             fleetOptions
		expectVersion string
		pendingFor    time.Duration
	)
	c := &cobra.Command{
		Use:     "report",
		Short:   "Shareable fleet report: overview plus what needs attention",
		Long:    reportLong,
		Example: reportExample,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f, err := reportFormat(o.output)
			if err != nil {
				return err
			}
			if expectVersion != "" && !versions.ValidVersion(expectVersion) {
				return fmt.Errorf("--expect-version %q must be a version like 1.30 or 1.30.4", expectVersion)
			}
			if pendingFor <= 0 {
				return fmt.Errorf("--pending-for must be more than 0")
			}
			cfg, err := kubeconfig.Load(kubeFlags)
			if err != nil {
				return err
			}
			sel, err := o.selectContexts(cmd.Context(), kubeFlags, cfg)
			if err != nil {
				return err
			}
			opts := report.Options{PendingFor: pendingFor, Now: time.Now()}
			work := fleet.Run(cmd.Context(), sel.refs, o.parallelism,
				func(ctx context.Context, r kubeconfig.ContextRef) (report.Cluster, error) {
					clients, err := k8s.New(kubeconfig.ForContext(kubeFlags, cfg, r.Name))
					if err != nil {
						return report.Cluster{}, err
					}
					src, err := pods.NewSource(clients.REST, clients.Typed)
					if err != nil {
						return report.Cluster{}, err
					}
					return report.Collect(ctx, clients.Typed, clients.Discovery, src, opts)
				})

			results := make([]report.Result, 0, len(work)+len(sel.failures)+len(sel.skipped))
			for _, res := range work {
				if res.Err != nil {
					results = append(results, report.Result{Context: res.Context, Cause: fleet.Cause(res.Err)})
					continue
				}
				c := res.Value
				results = append(results, report.Result{Context: res.Context, Cluster: &c})
			}
			for _, fl := range sel.failures {
				results = append(results, report.Result{Context: fl.Context, Cause: fleet.Cause(fl.Err)})
			}
			for _, name := range sel.skipped {
				results = append(results, report.Result{Context: name, Cause: skippedCause})
			}

			rep := report.Build(opts.Now, reportSelection(kubeFlags, &o, expectVersion, pendingFor), results)
			if err := report.Render(cmd.OutOrStdout(), rep, f); err != nil {
				return err
			}
			// Failures are in the report; stderr keeps the usual summary
			// (or full errors with --errors) for the operator.
			failures := append(append([]fleet.Failure(nil), sel.failures...), fleet.Failures(work)...)
			reportFailures(cmd.ErrOrStderr(), len(sel.refs)+len(sel.failures), failures, len(sel.skipped), o.errors)
			if rep.NeedsAttention() {
				return errAttention
			}
			return nil
		},
	}
	c.Flags().StringVar(&expectVersion, "expect-version", "", "flag control planes and kubelets not on this version (1.30 matches any 1.30.x)")
	c.Flags().DurationVar(&pendingFor, "pending-for", 5*time.Minute, "a pod Pending at least this long counts as stuck")
	o.addFlags(c, defaultParallelism)
	o.addErrorsFlag(c)
	out := c.Flags().Lookup("output")
	out.DefValue, out.Usage = "text", "output format: "+report.Formats
	o.output = "text"
	o.keepSkipped = true
	_ = c.Flags().MarkHidden("strict")
	c.Flags().Lookup("errors").Usage = "show each failed context's full error on stderr; the report itself only names the cause"
	_ = c.Flags().MarkHidden("no-headers")
	return c
}

func reportFormat(s string) (output.Format, error) {
	if strings.EqualFold(strings.TrimSpace(s), "text") {
		return report.FormatText, nil
	}
	f, err := output.ParseFormat(s)
	if err == nil && (f == output.FormatMarkdown || f == output.FormatHTML || f.Structured()) {
		return f, nil
	}
	return "", fmt.Errorf("report output must be one of %s, not %q", report.Formats, s)
}

func reportSelection(kubeFlags *genericclioptions.ConfigFlags, o *fleetOptions, expectVersion string, pendingFor time.Duration) report.Selection {
	sel := report.Selection{
		Contexts:        o.contexts,
		Exclude:         o.exclude,
		Versions:        o.versions,
		SkipUnreachable: o.skipUnreachable,
		ExpectVersion:   expectVersion,
		PendingFor:      shortDuration(pendingFor),
	}
	if kubeFlags.Context != nil && *kubeFlags.Context != "" {
		sel.Context = *kubeFlags.Context
	}
	return sel
}
