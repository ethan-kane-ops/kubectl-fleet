package cmd

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
)

func newGetCmd(kubeFlags *genericclioptions.ConfigFlags) *cobra.Command {
	var (
		filter        string
		parallelism   int
		labelSelector string
		allNamespaces bool
		outputFlag    string
	)
	c := &cobra.Command{
		Use:   "get <kind> [name]",
		Short: "Get a resource across all matching kubeconfig contexts",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := args[0]
			var name string
			if len(args) == 2 {
				name = args[1]
			}
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

			ns := ""
			if kubeFlags.Namespace != nil {
				ns = *kubeFlags.Namespace
			}
			if allNamespaces {
				ns = ""
			} else if ns == "" {
				ns = defaultNamespaceFromContexts(refs)
			}

			results := fleet.Run(cmd.Context(), refs, parallelism,
				func(ctx context.Context, r kubeconfig.ContextRef) ([]unstructured.Unstructured, error) {
					return getOne(ctx, kubeFlags, r, kind, name, ns, allNamespaces, labelSelector)
				})

			tbl := &output.Table{
				Headers:     []string{"CONTEXT", "NAMESPACE", "NAME", "AGE"},
				WideHeaders: []string{"KIND", "API_VERSION", "ERROR"},
			}
			for _, res := range results {
				if res.Err != nil {
					tbl.Append(
						[]string{res.Context, "", "<error>", ""},
						[]string{"", "", res.Err.Error()},
					)
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warn: %s: %v\n", res.Context, res.Err)
					continue
				}
				if len(res.Value) == 0 {
					continue
				}
				for _, u := range res.Value {
					tbl.Append(
						[]string{res.Context, u.GetNamespace(), u.GetName(), ageOf(u.GetCreationTimestamp())},
						[]string{u.GetKind(), u.GetAPIVersion(), ""},
					)
				}
			}
			return output.Print(cmd.OutOrStdout(), tbl, f)
		},
	}
	c.Flags().StringVar(&filter, "contexts", "", "regex applied to context names")
	c.Flags().IntVar(&parallelism, "parallelism", 8, "max parallel cluster calls (0=unbounded)")
	c.Flags().StringVarP(&labelSelector, "selector", "l", "", "label selector")
	c.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "query across all namespaces")
	c.Flags().StringVarP(&outputFlag, "output", "o", "table", "output format: table|wide|json|yaml")
	return c
}

func defaultNamespaceFromContexts(refs []kubeconfig.ContextRef) string {
	for _, r := range refs {
		if r.Namespace != "" {
			return r.Namespace
		}
	}
	return "default"
}

func getOne(ctx context.Context, kubeFlags *genericclioptions.ConfigFlags, r kubeconfig.ContextRef, kind, name, ns string, allNS bool, selector string) ([]unstructured.Unstructured, error) {
	restCfg, err := kubeconfig.RESTConfigFor(kubeFlags, r.Name)
	if err != nil {
		return nil, err
	}
	clients, err := k8s.New(restCfg)
	if err != nil {
		return nil, err
	}
	gvr, namespaced, err := k8s.ResolveGVR(clients.Discovery, kind)
	if err != nil {
		return nil, err
	}
	if !namespaced {
		ns = ""
	}
	if !namespaced || allNS {
		ns = ""
	}

	if name != "" {
		u, err := clients.Dynamic.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("get %s/%s: %w", gvr.Resource, name, err)
		}
		return []unstructured.Unstructured{*u}, nil
	}
	list, err := clients.Dynamic.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", gvr.Resource, err)
	}
	return list.Items, nil
}

func ageOf(ts metav1.Time) string {
	if ts.IsZero() {
		return ""
	}
	d := time.Since(ts.Time)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

