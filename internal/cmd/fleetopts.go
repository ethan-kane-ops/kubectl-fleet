package cmd

import (
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
)

// fleetOptions holds the flags every fleet command shares, so selection,
// parallelism and output mean the same thing on each of them.
type fleetOptions struct {
	contexts    string
	exclude     string
	parallelism int
	strict      bool
	output      string
	noHeaders   bool
}

func (o *fleetOptions) addFlags(c *cobra.Command, defaultParallelism int) {
	f := c.Flags()
	f.StringVar(&o.contexts, "contexts", "", "regex selecting kubeconfig contexts by name (default: all)")
	f.StringVar(&o.exclude, "exclude", "", "regex of context names to leave out, applied after --contexts")
	f.IntVar(&o.parallelism, "parallelism", defaultParallelism, "max contexts queried at once (0=unbounded)")
	f.BoolVar(&o.strict, "strict", false, "exit non-zero if any context failed")
	f.StringVarP(&o.output, "output", "o", "table", "output format: table|wide|json|yaml|name")
	f.BoolVar(&o.noHeaders, "no-headers", false, "suppress header row in table/wide output")
}

func (o *fleetOptions) format() (output.Format, error) {
	return output.ParseFormat(o.output)
}

// selectContexts applies --context, --contexts and --exclude to cfg.
func (o *fleetOptions) selectContexts(kubeFlags *genericclioptions.ConfigFlags, cfg *clientcmdapi.Config) ([]kubeconfig.ContextRef, error) {
	return kubeconfig.Select(kubeFlags, cfg, o.contexts, o.exclude)
}
