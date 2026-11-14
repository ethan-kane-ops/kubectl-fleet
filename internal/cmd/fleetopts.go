package cmd

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/versions"
)

// fleetOptions holds the flags every fleet command shares, so selection,
// parallelism and output mean the same thing on each of them.
type fleetOptions struct {
	contexts    []string
	exclude     string
	parallelism int
	strict      bool
	output      string
	noHeaders   bool
	errors      bool
	// versions keeps contexts whose server version matches any of them.
	versions []string
	// skipUnreachable leaves out contexts whose API server can't be
	// reached, as a skip rather than a failure.
	skipUnreachable bool
	// unreachableOnly keeps only the contexts that can't be reached
	// (contexts --unreachable).
	unreachableOnly bool
	// keepSkipped returns a selection even when --skip-unreachable skipped
	// every context, for report, which lists skipped contexts as failed.
	keepSkipped bool
	// probeTimeout, when set, bounds each probe instead of
	// --request-timeout or probeTimeout (contexts --timeout sets it).
	probeTimeout time.Duration
}

func (o *fleetOptions) addFlags(c *cobra.Command, defaultParallelism int) {
	f := c.Flags()
	f.StringArrayVar(&o.contexts, "contexts", nil, "regex selecting kubeconfig contexts by name; repeatable, any may match (default: all)")
	f.StringVar(&o.exclude, "exclude", "", "regex of context names to leave out, applied after --contexts")
	f.IntVar(&o.parallelism, "parallelism", defaultParallelism, "max contexts queried at once (0=unbounded)")
	f.BoolVar(&o.strict, "strict", false, "exit non-zero if any context failed")
	f.StringVarP(&o.output, "output", "o", "table", "output format: table|wide|json|yaml|name|markdown|html")
	f.BoolVar(&o.noHeaders, "no-headers", false, "suppress header row in table/wide output")
	f.StringArrayVar(&o.versions, "version", nil, "keep contexts whose server version matches; repeatable, any may match (1.29 = any 1.29.x)")
	if c.Name() != "contexts" {
		f.BoolVar(&o.skipUnreachable, "skip-unreachable", false, "leave out contexts whose API server cannot be reached, as a skip rather than a failure")
	}
}

// addErrorsFlag registers --errors on the commands that summarise failures.
func (o *fleetOptions) addErrorsFlag(c *cobra.Command) {
	c.Flags().BoolVar(&o.errors, "errors", false, "show each failed context: an <error> row with its ERROR text, and one warning line per failure (default: a one-line summary)")
}

// finish reports every failure (from the probe and from the work itself) and
// applies --strict. attempted is how many contexts the work ran against.
func (o *fleetOptions) finish(w io.Writer, sel selection, work []fleet.Failure, attempted int) error {
	failures := append(append([]fleet.Failure(nil), sel.failures...), work...)
	total := attempted + len(sel.failures)
	reportFailures(w, total, failures, len(sel.skipped), o.errors)
	switch {
	case total > 0 && len(failures) == total:
		// Nothing could be queried: an empty result would be
		// indistinguishable from "nothing found", so fail even without
		// --strict, as kubectl does against an unreachable cluster.
		return fmt.Errorf("all %d contexts failed", total)
	case o.strict && len(failures) > 0:
		return fmt.Errorf("%d of %d contexts failed", len(failures), total)
	}
	return nil
}

// include combines the --contexts regexes into one: a context matches if it
// matches any of them. Each is compiled on its own first, so a bad one is
// named in the error.
func (o *fleetOptions) include() (string, error) {
	for _, re := range o.contexts {
		if _, err := regexp.Compile(re); err != nil {
			return "", fmt.Errorf("compile --contexts %q: %w", re, err)
		}
	}
	switch len(o.contexts) {
	case 0:
		return "", nil
	case 1:
		return o.contexts[0], nil
	}
	parts := make([]string, len(o.contexts))
	for i, re := range o.contexts {
		parts[i] = "(?:" + re + ")"
	}
	return strings.Join(parts, "|"), nil
}

func (o *fleetOptions) format() (output.Format, error) {
	return output.ParseFormat(o.output)
}

// probeTimeout bounds each probe request when --request-timeout is
// not set, so one dead cluster cannot stall the selection pass.
const probeTimeout = 10 * time.Second

// probeResult is what the probe pass learned about one context.
type probeResult struct {
	version string
	latency time.Duration
	err     error
}

// selection is the outcome of picking contexts: the ones to run against,
// the ones the probe could not read, and the unreachable ones
// --skip-unreachable left out.
type selection struct {
	refs     []kubeconfig.ContextRef
	failures []fleet.Failure
	// skipped names the unreachable contexts --skip-unreachable left out.
	skipped []string
	// probes holds the probe per context name; nil when nothing probed.
	probes map[string]probeResult
}

// needsProbe reports whether selection has to call each context first.
func (o *fleetOptions) needsProbe() bool {
	return len(o.versions) > 0 || o.skipUnreachable || o.unreachableOnly
}

// selectContexts applies --context, --contexts, --exclude, --version,
// --skip-unreachable and contexts --unreachable.
func (o *fleetOptions) selectContexts(ctx context.Context, kubeFlags *genericclioptions.ConfigFlags, cfg *clientcmdapi.Config) (selection, error) {
	for _, v := range o.versions {
		if !versions.ValidVersion(v) {
			return selection{}, fmt.Errorf("--version %q must be a version like 1.30 or 1.30.4", v)
		}
	}
	include, err := o.include()
	if err != nil {
		return selection{}, err
	}
	refs, err := kubeconfig.Select(kubeFlags, cfg, include, o.exclude)
	if err != nil {
		return selection{}, err
	}
	if len(refs) == 0 {
		return selection{}, fmt.Errorf("no matching contexts")
	}
	if !o.needsProbe() {
		return selection{refs: refs}, nil
	}

	results := fleet.Run(ctx, refs, o.parallelism, func(_ context.Context, r kubeconfig.ContextRef) (probeResult, error) {
		return probeContext(kubeconfig.ForContext(kubeFlags, cfg, r.Name), o.probeTimeout), nil
	})
	sel := selection{probes: make(map[string]probeResult, len(refs))}
	for i, res := range results {
		ref, p := refs[i], res.Value
		sel.probes[ref.Name] = p
		// Only a network failure means unreachable; a 401 or 403 is a
		// reachable server refusing us, which must not be skipped.
		unreachable := p.err != nil && networkFailure(p.err)
		switch {
		case o.unreachableOnly:
			if unreachable {
				sel.refs = append(sel.refs, ref)
			}
		case unreachable && o.skipUnreachable:
			sel.skipped = append(sel.skipped, ref.Name)
		case p.err != nil:
			sel.failures = append(sel.failures, fleet.Failure{Context: ref.Name, Err: p.err})
		case o.matchesVersion(p.version):
			sel.refs = append(sel.refs, ref)
		}
	}
	switch {
	case len(sel.refs) > 0 || len(sel.failures) > 0, o.unreachableOnly:
		// For --unreachable an empty selection is an answer: nothing is down.
		return sel, nil
	case len(sel.skipped) == len(refs) && o.keepSkipped:
		return sel, nil
	case len(sel.skipped) == len(refs):
		return selection{}, fmt.Errorf("all %d selected contexts are unreachable (skipped by --skip-unreachable)", len(refs))
	}
	return selection{}, fmt.Errorf("no matching contexts: --version matched none of %d", len(refs)-len(sel.skipped))
}

// matchesVersion reports whether a server version matches any --version;
// with none given, every version does.
func (o *fleetOptions) matchesVersion(git string) bool {
	if len(o.versions) == 0 {
		return true
	}
	for _, v := range o.versions {
		if versions.MatchVersion(git, v) {
			return true
		}
	}
	return false
}

// networkFailure reports a probe error that means the API server could not
// be reached at all.
func networkFailure(err error) bool {
	switch fleet.Cause(err) {
	case "timed out", "connection refused", "DNS lookup failed":
		return true
	}
	return false
}

// probeContext reads a context's server version, timing the call.
func probeContext(kc *kubeconfig.Context, timeout time.Duration) probeResult {
	restCfg, err := kc.ToRESTConfig()
	if err != nil {
		return probeResult{err: err}
	}
	switch {
	case timeout > 0:
		restCfg.Timeout = timeout
	case restCfg.Timeout == 0:
		restCfg.Timeout = probeTimeout
	}
	disco, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		return probeResult{err: fmt.Errorf("discovery client: %w", err)}
	}
	start := time.Now()
	v, err := disco.ServerVersion()
	res := probeResult{latency: time.Since(start)}
	if err != nil {
		res.err = fmt.Errorf("server version: %w", err)
		return res
	}
	res.version = v.GitVersion
	return res
}
