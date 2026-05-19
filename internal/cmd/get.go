package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/cli-runtime/pkg/genericclioptions"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/printers"
)

type fetchedResource struct {
	items      []unstructured.Unstructured
	namespaced bool
	resource   string
}

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
				func(ctx context.Context, r kubeconfig.ContextRef) (fetchedResource, error) {
					return getOne(ctx, kubeFlags, r, kind, name, ns, allNamespaces, labelSelector)
				})

			schema, namespaced := schemaForResults(kind, results)
			tbl := buildTable(schema, namespaced)
			for _, res := range results {
				if res.Err != nil {
					appendErrorRow(tbl, res.Context, namespaced, schema, res.Err)
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warn: %s: %v\n", res.Context, res.Err)
					continue
				}
				for i := range res.Value.items {
					u := &res.Value.items[i]
					cols, wide := schema.Row(u)
					prefix := []string{res.Context}
					if namespaced {
						prefix = append(prefix, u.GetNamespace())
					}
					tbl.Append(append(prefix, cols...), append(wide, ""))
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

// schemaForResults picks a per-kind schema based on the resolved GVR resource
// from the first successful cluster. Falls back to the user-supplied alias,
// then to printers.Generic. When no cluster succeeded the per-kind schema's
// own Namespaced declaration drives whether the NAMESPACE column renders.
func schemaForResults(kind string, results []fleet.ClusterResult[fetchedResource]) (printers.Schema, bool) {
	var resource string
	var liveNamespaced bool
	var anySuccess bool
	for _, r := range results {
		if r.Err == nil && r.Value.resource != "" {
			resource = r.Value.resource
			liveNamespaced = r.Value.namespaced
			anySuccess = true
			break
		}
	}
	if resource != "" {
		if s, ok := printers.For(resource); ok {
			return s, liveNamespaced
		}
	}
	if s, ok := printers.For(kind); ok {
		if anySuccess {
			return s, liveNamespaced
		}
		return s, s.Namespaced
	}
	g := printers.Generic()
	if anySuccess {
		return g, liveNamespaced
	}
	return g, g.Namespaced
}

func buildTable(s printers.Schema, namespaced bool) *output.Table {
	headers := []string{"CONTEXT"}
	if namespaced {
		headers = append(headers, "NAMESPACE")
	}
	headers = append(headers, s.Headers...)
	wide := append([]string{}, s.WideHeaders...)
	wide = append(wide, "ERROR")
	return &output.Table{Headers: headers, WideHeaders: wide}
}

func appendErrorRow(tbl *output.Table, ctx string, namespaced bool, s printers.Schema, err error) {
	row := []string{ctx}
	if namespaced {
		row = append(row, "")
	}
	// First per-kind column holds "<error>" so the default table view stays
	// informative without forcing -o wide.
	for i := range s.Headers {
		if i == 0 {
			row = append(row, "<error>")
		} else {
			row = append(row, "")
		}
	}
	wide := make([]string, len(s.WideHeaders))
	wide = append(wide, err.Error())
	tbl.Append(row, wide)
}

func getOne(ctx context.Context, kubeFlags *genericclioptions.ConfigFlags, r kubeconfig.ContextRef, kind, name, ns string, allNS bool, selector string) (fetchedResource, error) {
	restCfg, err := kubeconfig.RESTConfigFor(kubeFlags, r.Name)
	if err != nil {
		return fetchedResource{}, err
	}
	clients, err := k8s.New(restCfg)
	if err != nil {
		return fetchedResource{}, err
	}
	gvr, namespaced, err := k8s.ResolveGVR(clients.Discovery, kind)
	if err != nil {
		return fetchedResource{}, err
	}
	out := fetchedResource{namespaced: namespaced, resource: gvr.Resource}
	if !namespaced || allNS {
		ns = ""
	}

	if name != "" {
		u, err := clients.Dynamic.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return out, fmt.Errorf("get %s/%s: %w", gvr.Resource, name, err)
		}
		out.items = []unstructured.Unstructured{*u}
		return out, nil
	}
	list, err := clients.Dynamic.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		return out, fmt.Errorf("list %s: %w", gvr.Resource, err)
	}
	out.items = list.Items
	return out, nil
}
