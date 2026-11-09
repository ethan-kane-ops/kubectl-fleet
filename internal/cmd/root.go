// Package cmd implements the cobra command tree for the kubectl-fleet plugin.
package cmd

import (
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
)

// Default --parallelism. Contexts are independent API servers, so the limit
// guards local resources, not any one server. status, version and contexts
// keep little per context in flight; get decodes a page of up to 500 objects
// per in-flight context (tens of MB on large clusters), so it runs fewer.
const (
	defaultParallelism    = 32
	defaultGetParallelism = 16
)

// NewRootCmd returns the root cobra command for kubectl-fleet.
//
// Binary is invoked by kubectl as `kubectl fleet <subcommand>` once
// the binary is on PATH.
func NewRootCmd() *cobra.Command {
	kubeFlags := genericclioptions.NewConfigFlags(true)

	root := &cobra.Command{
		// Use is the single-token binary name, not "kubectl fleet": cobra's
		// generated shell completion scripts register against Name() (the
		// first word of Use) verbatim. "kubectl fleet" here would produce
		// `complete -F __start_kubectl kubectl`, hijacking real kubectl's
		// own tab-completion instead of scoping to this plugin.
		Use:           "kubectl-fleet",
		Short:         "Multi-cluster operational awareness for K8s fleets",
		Long:          rootLong,
		Example:       rootExample,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(*cobra.Command, []string) error {
			return kubeconfig.ValidateFleetFlags(kubeFlags)
		},
		// Help/usage text should still read "kubectl fleet ..." since
		// that's the actual invocation via the kubectl plugin protocol.
		Annotations: map[string]string{
			cobra.CommandDisplayNameAnnotation: "kubectl fleet",
		},
	}

	kubeFlags.AddFlags(root.PersistentFlags())

	root.AddCommand(
		newContextsCmd(kubeFlags),
		newGetCmd(kubeFlags),
		newStatusCmd(kubeFlags),
		newVersionCmd(kubeFlags),
	)
	registerCompletions(root, kubeFlags)

	return root
}
