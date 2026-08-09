// Package cmd implements the cobra command tree for the kubectl-fleet plugin.
package cmd

import (
	"github.com/spf13/cobra"
	"k8s.io/cli-runtime/pkg/genericclioptions"
)

// NewRootCmd returns the root cobra command for kubectl-fleet.
//
// Binary is invoked by kubectl as `kubectl fleet <subcommand>` once
// the binary is on PATH.
func NewRootCmd() *cobra.Command {
	kubeFlags := genericclioptions.NewConfigFlags(true)

	root := &cobra.Command{
		Use:           "kubectl fleet",
		Short:         "Multi-cluster operational awareness for K8s fleets",
		SilenceUsage:  true,
		SilenceErrors: true,
		// cobra.Command.Name() only takes the first word of Use, so without
		// this every subcommand's usage line renders as "kubectl <verb>"
		// instead of "kubectl fleet <verb>". This is cobra's documented fix
		// for multi-word plugin invocations.
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

	return root
}
