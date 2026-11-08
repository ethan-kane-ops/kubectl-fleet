package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/tools/pager"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/fleet"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/printers"
)

// fetchedResource is one context's result, already projected into the
// output format while paging so no context holds its objects in memory:
// table rows (NAMESPACE, when namespaced, then the schema's columns) or
// compact JSON for -o json|yaml.
type fetchedResource struct {
	namespaced bool
	resource   string
	group      string
	count      int
	rows       [][]string
	wide       [][]string
	encoded    [][]byte
}

// getRequest is what `get` asks of every context.
type getRequest struct {
	kind, name, selector string
	allNamespaces        bool
	format               output.Format
}

func newGetCmd(kubeFlags *genericclioptions.ConfigFlags) *cobra.Command {
	var (
		filter        string
		parallelism   int
		labelSelector string
		allNamespaces bool
		outputFlag    string
		noHeaders     bool
		strict        bool
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
			if name != "" && labelSelector != "" {
				return fmt.Errorf("a resource name cannot be combined with --selector")
			}
			f, err := output.ParseFormat(outputFlag)
			if err != nil {
				return err
			}
			cfg, err := kubeconfig.Load(kubeFlags)
			if err != nil {
				return err
			}
			refs, err := kubeconfig.Select(kubeFlags, cfg, filter)
			if err != nil {
				return err
			}
			if len(refs) == 0 {
				return fmt.Errorf("no matching contexts")
			}

			req := getRequest{kind: kind, name: name, selector: labelSelector, allNamespaces: allNamespaces, format: f}
			fetch := func(ctx context.Context, r kubeconfig.ContextRef) (fetchedResource, error) {
				return getOne(ctx, kubeconfig.ForContext(kubeFlags, cfg, r.Name), req)
			}

			// json/yaml for a list is written as each context completes, in
			// context order, so the fleet's objects never sit in memory at
			// once. A named lookup stays buffered: it is small, and nothing is
			// printed when no context has the object.
			var lw *output.ListWriter
			if f.Structured() && name == "" {
				if lw, err = output.NewListWriter(cmd.OutOrStdout(), f); err != nil {
					return err
				}
			}
			var (
				collected []fleet.ClusterResult[fetchedResource] // table, wide, name
				encoded   [][]byte                               // buffered json/yaml
				chosen    *schema.GroupResource
				found     int
				failed    int
				writeErr  error
			)
			fleet.Stream(cmd.Context(), refs, parallelism, fetch, func(res fleet.ClusterResult[fetchedResource]) {
				// Rows are projected with each context's own schema; a context
				// that resolves the kind to another group/resource than the
				// first successful one would not line up, so it is reported.
				if res.Err == nil {
					gr := schema.GroupResource{Group: res.Value.group, Resource: res.Value.resource}
					if chosen == nil {
						chosen = &gr
					} else if gr != *chosen {
						res.Err = fmt.Errorf("%s resolves to %s here, not %s", kind, gr, *chosen)
					}
				}
				if res.Err != nil {
					failed++
					_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "warn: %s: %v\n", res.Context, res.Err)
				} else {
					found += res.Value.count
				}
				switch {
				case !f.Structured():
					collected = append(collected, res)
				case res.Err != nil:
				case lw != nil:
					for _, item := range res.Value.encoded {
						if writeErr == nil {
							writeErr = lw.Add(item)
						}
					}
				default:
					encoded = append(encoded, res.Value.encoded...)
				}
			})
			if writeErr != nil {
				return writeErr
			}

			switch {
			case name != "" && found == 0:
				return fmt.Errorf("%s %q not found in any reachable context", kind, name)
			case lw != nil:
				err = lw.Close()
			case f.Structured():
				err = writeList(cmd.OutOrStdout(), encoded, f)
			default:
				err = output.Print(cmd.OutOrStdout(), buildGetTable(kind, collected, f, noHeaders), f)
			}
			if err != nil {
				return err
			}
			if strict && failed > 0 {
				return fmt.Errorf("%d of %d contexts failed", failed, len(refs))
			}
			return nil
		},
	}
	c.Flags().StringVar(&filter, "contexts", "", "regex applied to context names")
	c.Flags().IntVar(&parallelism, "parallelism", defaultGetParallelism, "max parallel cluster calls (0=unbounded); each in-flight context holds a page of up to 500 decoded objects")
	c.Flags().StringVarP(&labelSelector, "selector", "l", "", "label selector")
	c.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "query across all namespaces")
	c.Flags().StringVarP(&outputFlag, "output", "o", "table", "output format: table|wide|json|yaml|name")
	c.Flags().BoolVar(&noHeaders, "no-headers", false, "suppress header row in table/wide output")
	c.Flags().BoolVar(&strict, "strict", false, "exit non-zero if any context failed")
	return c
}

// ContextAnnotation names the kubeconfig context an object came from in
// `get -o json|yaml` output.
const ContextAnnotation = "kubectl-fleet/context"

// buildGetTable lays out the collected per-context rows under the header of
// the first successful context's schema.
func buildGetTable(kind string, results []fleet.ClusterResult[fetchedResource], f output.Format, noHeaders bool) *output.Table {
	s, namespaced, _ := schemaForResults(kind, results)
	tbl := buildTable(s, namespaced)
	tbl.NoHeaders = noHeaders
	for _, res := range results {
		if res.Err != nil {
			// -o name feeds scripts; an "<error>" placeholder there would read
			// as an object name. The stderr warning reports it.
			if f != output.FormatName {
				appendErrorRow(tbl, res.Context, namespaced, s, res.Err)
			}
			continue
		}
		for j, row := range res.Value.rows {
			var wide []string
			if j < len(res.Value.wide) {
				wide = res.Value.wide[j]
			}
			tbl.Append(append([]string{res.Context}, row...), wide)
		}
	}
	return tbl
}

func writeList(w io.Writer, items [][]byte, f output.Format) error {
	lw, err := output.NewListWriter(w, f)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := lw.Add(item); err != nil {
			return err
		}
	}
	return lw.Close()
}

// annotateContext records on u the context it came from, so objects with the
// same name on different clusters stay distinguishable. u is owned by this
// run, so it is annotated in place rather than copied.
func annotateContext(u *unstructured.Unstructured, ctxName string) {
	ann := u.GetAnnotations()
	if ann == nil {
		ann = map[string]string{}
	}
	ann[ContextAnnotation] = ctxName
	u.SetAnnotations(ann)
}

// schemaForResults picks a per-kind schema for the group and resource the
// first successful cluster resolved, else printers.Generic, and returns that
// group/resource. When no cluster succeeded it falls back to the user's input
// (alias or resource.group) and the schema's own Namespaced declaration.
func schemaForResults(kind string, results []fleet.ClusterResult[fetchedResource]) (printers.Schema, bool, schema.GroupResource) {
	for _, r := range results {
		if r.Err == nil && r.Value.resource != "" {
			gr := schema.GroupResource{Group: r.Value.group, Resource: r.Value.resource}
			return schemaFor(gr), r.Value.namespaced, gr
		}
	}
	s, ok := printers.For(kind)
	if gr := schema.ParseGroupResource(kind); gr.Group != "" {
		s, ok = printers.ForGroupResource(gr.Group, gr.Resource)
	}
	if !ok {
		s = printers.Generic()
	}
	return s, s.Namespaced, schema.GroupResource{}
}

// schemaFor returns the per-kind schema registered for gr, else Generic.
func schemaFor(gr schema.GroupResource) printers.Schema {
	if s, ok := printers.ForGroupResource(gr.Group, gr.Resource); ok {
		return s
	}
	return printers.Generic()
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

// getOne fetches req.kind (and optionally req.name) from one context and
// projects each object into req.format as it arrives. Without -A the
// namespace is resolved per context: --namespace, else that context's own
// namespace, else "default".
func getOne(ctx context.Context, kc *kubeconfig.Context, req getRequest) (fetchedResource, error) {
	clients, err := k8s.New(kc)
	if err != nil {
		return fetchedResource{}, err
	}
	gvr, namespaced, err := k8s.ResolveGVR(clients.Discovery, req.kind)
	if err != nil {
		return fetchedResource{}, err
	}
	out := fetchedResource{namespaced: namespaced, resource: gvr.Resource, group: gvr.Group}
	ns := ""
	if namespaced && !req.allNamespaces {
		if ns, err = kc.Namespace(); err != nil {
			return out, err
		}
	}

	s := schemaFor(gvr.GroupResource())
	add := func(u *unstructured.Unstructured) error {
		out.count++
		if req.format.Structured() {
			annotateContext(u, kc.Name())
			b, err := json.Marshal(u.Object)
			if err != nil {
				return fmt.Errorf("encode %s/%s: %w", gvr.Resource, u.GetName(), err)
			}
			out.encoded = append(out.encoded, b)
			return nil
		}
		cols, wide := s.Row(u)
		if namespaced {
			cols = append([]string{u.GetNamespace()}, cols...)
		}
		out.rows = append(out.rows, cols)
		if req.format == output.FormatWide {
			out.wide = append(out.wide, append(wide, ""))
		}
		return nil
	}

	// A namespaced name with -A has no single object URL, so it becomes a
	// list across all namespaces filtered by name: it finds every namespace,
	// on every cluster, that holds an object of that name.
	listOpts := metav1.ListOptions{LabelSelector: req.selector}
	if req.name != "" && namespaced && req.allNamespaces {
		listOpts.FieldSelector = fields.OneTermEqualSelector("metadata.name", req.name).String()
	} else if req.name != "" {
		u, err := clients.Dynamic.Resource(gvr).Namespace(ns).Get(ctx, req.name, metav1.GetOptions{})
		// Absent from some clusters is normal for a fleet, not a failure.
		if apierrors.IsNotFound(err) {
			return out, nil
		}
		if err != nil {
			return out, fmt.Errorf("get %s/%s: %w", gvr.Resource, req.name, err)
		}
		return out, add(u)
	}
	// Page through the list as kubectl does (500 per request) so large
	// clusters are not asked to serialise every object in one response.
	lister := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		return clients.Dynamic.Resource(gvr).Namespace(ns).List(ctx, opts)
	})
	err = lister.EachListItem(ctx, listOpts, func(obj runtime.Object) error {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			return fmt.Errorf("unexpected list item %T", obj)
		}
		return add(u)
	})
	if err != nil {
		return out, fmt.Errorf("list %s: %w", gvr.Resource, err)
	}
	return out, nil
}
