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
	}

	kubeFlags.AddFlags(root.PersistentFlags())

	// Register subcommands here as the plugin grows.
	// root.AddCommand(newFooCmd(kubeFlags))

	return root
}
