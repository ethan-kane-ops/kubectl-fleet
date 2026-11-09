package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

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
// table rows under this context's own columns, or compact JSON for
// -o json|yaml. Rows start with the namespace when namespaced; wide rows
// are kept only for -o wide.
type fetchedResource struct {
	namespaced  bool
	resource    string
	group       string
	count       int
	columns     []string
	wideColumns []string
	rows        [][]string
	wide        [][]string
	encoded     [][]byte
}

// getRequest is what `get` asks of every context.
type getRequest struct {
	kind, name, selector string
	allNamespaces        bool
	serverPrint          bool
	format               output.Format
}

func newGetCmd(kubeFlags *genericclioptions.ConfigFlags) *cobra.Command {
	var (
		o             fleetOptions
		labelSelector string
		allNamespaces bool
		serverPrint   bool
		problemsOnly  bool
	)
	c := &cobra.Command{
		Use:     "get KIND [NAME]",
		Short:   "Get a resource across all matching kubeconfig contexts",
		Long:    getLong,
		Example: getExample,
		Args:    cobra.RangeArgs(1, 2),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			switch len(args) {
			case 0:
				return completeKinds(kubeFlags, &o), noFiles
			case 1:
				return completeNames(kubeFlags, &o, args[0], allNamespaces), noFiles
			}
			return nil, noFiles
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			kind := args[0]
			var name string
			if len(args) == 2 {
				name = args[1]
			}
			if name != "" && labelSelector != "" {
				return fmt.Errorf("a resource name cannot be combined with --selector")
			}
			f, err := o.format()
			if err != nil {
				return err
			}
			if problemsOnly && f.Structured() {
				return fmt.Errorf("--problems works with table, wide and name output")
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

			req := getRequest{kind: kind, name: name, selector: labelSelector, allNamespaces: allNamespaces, serverPrint: serverPrint, format: f}
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
				anyOK     bool                                   // --problems: some context answered
				anyJudged bool                                   // --problems: some context had STATUS or READY
				encoded   [][]byte                               // buffered json/yaml
				chosen    *schema.GroupResource
				found     int
				failures  []fleet.Failure
				writeErr  error
			)
			fleet.Stream(cmd.Context(), refs, o.parallelism, fetch, func(res fleet.ClusterResult[fetchedResource]) {
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
					failures = append(failures, fleet.Failure{Context: res.Context, Err: res.Err})
				} else {
					found += res.Value.count
				}
				switch {
				case !f.Structured():
					if problemsOnly && res.Err == nil {
						anyOK = true
						if keepProblems(&res.Value) {
							anyJudged = true
						}
					}
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
			if problemsOnly && anyOK && !anyJudged {
				return fmt.Errorf("--problems needs a READY or STATUS column; %s has neither", kind)
			}

			switch {
			case name != "" && found == 0:
				_ = o.finish(cmd.ErrOrStderr(), sel, failures, len(refs))
				return fmt.Errorf("%s %q not found in any reachable context", kind, name)
			case lw != nil:
				err = lw.Close()
			case f.Structured():
				err = writeList(cmd.OutOrStdout(), encoded, f)
			default:
				err = output.Print(cmd.OutOrStdout(), buildGetTable(kind, collected, f, o.noHeaders, o.errors), f)
			}
			if err != nil {
				return err
			}
			return o.finish(cmd.ErrOrStderr(), sel, failures, len(refs))
		},
	}
	c.Flags().StringVarP(&labelSelector, "selector", "l", "", "label selector")
	c.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "query across all namespaces")
	c.Flags().BoolVar(&problemsOnly, "problems", false, "show only unhealthy rows: STATUS not healthy (Running, Completed, Ready, Bound…) or READY short (1/2)")
	c.Flags().BoolVar(&serverPrint, "server-print", true, "let each API server render the table columns, as kubectl does; false uses built-in columns")
	o.addFlags(c, defaultGetParallelism)
	o.addErrorsFlag(c)
	c.Flags().Lookup("parallelism").Usage = "max contexts queried at once (0=unbounded); each in-flight context holds a page of up to 500 decoded objects"
	return c
}

// keepProblems trims a context's rows to the unhealthy ones (problemRow).
// It returns false, keeping no rows, when the context's columns have neither
// STATUS nor READY to judge by.
func keepProblems(v *fetchedResource) bool {
	if !slices.Contains(v.columns, "STATUS") && !slices.Contains(v.columns, "READY") {
		v.rows, v.wide = nil, nil
		return false
	}
	off := 0
	if v.namespaced {
		off = 1
	}
	var rows, wide [][]string
	for j, row := range v.rows {
		if problem, _ := problemRow(v.columns, row[min(off, len(row)):]); problem {
			rows = append(rows, row)
			if j < len(v.wide) {
				wide = append(wide, v.wide[j])
			}
		}
	}
	v.rows, v.wide = rows, wide
	return true
}

// ContextAnnotation names the kubeconfig context an object came from in
// `get -o json|yaml` output.
const ContextAnnotation = "kubectl-fleet/context"

// buildGetTable lays out the collected per-context rows. Contexts can
// render different columns for the same kind (another Kubernetes or CRD
// version), so columns are merged by name, each keeping its place relative
// to its neighbours, and a context leaves blank the columns it lacks. With no successful context the header
// falls back to the kind's built-in columns.
//
// Failed contexts become <error> rows only with showErrors (--errors), which
// also moves the ERROR column into the regular columns.
func buildGetTable(kind string, results []fleet.ClusterResult[fetchedResource], f output.Format, noHeaders, showErrors bool) *output.Table {
	var cols, wideCols columnSet
	namespaced, anyOK := false, false
	for _, res := range results {
		if res.Err == nil {
			if !anyOK {
				namespaced, anyOK = res.Value.namespaced, true
			}
			cols.add(res.Value.columns...)
		}
	}
	for _, res := range results {
		if res.Err == nil {
			for _, c := range res.Value.wideColumns {
				if !cols.has(c) {
					wideCols.add(c)
				}
			}
		}
	}
	if !anyOK {
		s := fallbackSchema(kind)
		namespaced = s.Namespaced
		cols.add(s.Headers...)
		wideCols.add(s.WideHeaders...)
	}

	headers := []string{"CONTEXT"}
	if namespaced {
		headers = append(headers, "NAMESPACE")
	}
	tbl := &output.Table{
		Headers:     append(headers, cols.names...),
		WideHeaders: append(append([]string{}, wideCols.names...), "ERROR"),
		NoHeaders:   noHeaders,
	}
	prefix := len(headers)
	for _, res := range results {
		if res.Err != nil {
			// -o name feeds scripts; an "<error>" placeholder there would read
			// as an object name. The stderr warning reports it.
			if showErrors && f != output.FormatName {
				row := make([]string, len(tbl.Headers))
				row[0], row[prefix] = res.Context, "<error>"
				wide := make([]string, len(tbl.WideHeaders))
				wide[len(wide)-1] = res.Err.Error()
				tbl.Append(row, wide)
			}
			continue
		}
		v := res.Value
		nsCols := 0
		if v.namespaced {
			nsCols = 1
		}
		for j, src := range v.rows {
			row := make([]string, len(tbl.Headers))
			wide := make([]string, len(tbl.WideHeaders))
			row[0] = res.Context
			if nsCols == 1 && namespaced {
				row[1] = src[0]
			}
			place := func(name, cell string) {
				if i, ok := cols.index[name]; ok {
					row[prefix+i] = cell
				} else if i, ok := wideCols.index[name]; ok {
					wide[i] = cell
				}
			}
			for i, name := range v.columns {
				if nsCols+i < len(src) {
					place(name, src[nsCols+i])
				}
			}
			if j < len(v.wide) {
				for i, name := range v.wideColumns {
					if i < len(v.wide[j]) {
						place(name, v.wide[j][i])
					}
				}
			}
			tbl.Append(row, wide)
		}
	}
	if showErrors {
		promoteErrorColumn(tbl)
	}
	return tbl
}

// columnSet is an ordered set of column names.
type columnSet struct {
	names []string
	index map[string]int
}

// add merges one context's ordered columns: a column not yet in the set is
// inserted right after the column that precedes it in names, so a column a
// newer cluster adds keeps its place (OWNER before AGE, not after it).
func (c *columnSet) add(names ...string) {
	prev := -1
	for k, n := range names {
		if i, ok := c.index[n]; ok {
			prev = i
			continue
		}
		at := prev + 1
		if prev < 0 {
			// No known column before it: go just before the next known one,
			// or at the end when none of this context's columns are known.
			at = len(c.names)
			for _, next := range names[k+1:] {
				if i, ok := c.index[next]; ok {
					at = i
					break
				}
			}
		}
		prev = at
		c.names = append(c.names[:at], append([]string{n}, c.names[at:]...)...)
		c.index = make(map[string]int, len(c.names))
		for i, name := range c.names {
			c.index[name] = i
		}
	}
}

func (c *columnSet) has(name string) bool {
	_, ok := c.index[name]
	return ok
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

// fallbackSchema is the built-in schema for the user's kind (alias or
// resource.group), used for the header when no context succeeded.
func fallbackSchema(kind string) printers.Schema {
	s, ok := printers.For(kind)
	if gr := schema.ParseGroupResource(kind); gr.Group != "" {
		s, ok = printers.ForGroupResource(gr.Group, gr.Resource)
	}
	if !ok {
		return printers.Generic()
	}
	return s
}

// schemaFor returns the per-kind schema registered for gr, else Generic.
func schemaFor(gr schema.GroupResource) printers.Schema {
	if s, ok := printers.ForGroupResource(gr.Group, gr.Resource); ok {
		return s
	}
	return printers.Generic()
}

// getOne fetches req.kind (and optionally req.name) from one context and
// projects each object into req.format as it arrives. Table output uses the
// server-rendered table (kubectl's columns, including a CRD's printer
// columns) unless --server-print=false or the server cannot render one, in
// which case the built-in printers are used. Without -A the namespace is
// resolved per context: --namespace, else that context's own namespace, else
// "default".
func getOne(ctx context.Context, kc *kubeconfig.Context, req getRequest) (fetchedResource, error) {
	clients, err := k8s.New(kc)
	if err != nil {
		return fetchedResource{}, err
	}
	gvr, namespaced, err := k8s.ResolveGVR(clients.Discovery, req.kind)
	if err != nil {
		return fetchedResource{}, err
	}
	base := fetchedResource{namespaced: namespaced, resource: gvr.Resource, group: gvr.Group}
	ns := ""
	if namespaced && !req.allNamespaces {
		if ns, err = kc.Namespace(); err != nil {
			return base, err
		}
	}

	// A namespaced name with -A has no single object URL, so it becomes a
	// list across all namespaces filtered by name: it finds every namespace,
	// on every cluster, that holds an object of that name.
	listOpts := metav1.ListOptions{LabelSelector: req.selector}
	getByName := req.name != ""
	if getByName && namespaced && req.allNamespaces {
		listOpts.FieldSelector = fields.OneTermEqualSelector("metadata.name", req.name).String()
		getByName = false
	}

	if req.serverPrint && !req.format.Structured() {
		out := base
		err := fetchServerTable(ctx, clients, gvr, ns, req, getByName, listOpts, &out)
		if !errors.Is(err, k8s.ErrNoTable) {
			return out, err
		}
	}
	out := base
	return out, fetchObjects(ctx, clients, gvr, kc.Name(), ns, req, getByName, listOpts, &out)
}

// fetchServerTable fills out from the server-rendered table. It returns
// k8s.ErrNoTable, with nothing kept, when the server cannot render one.
func fetchServerTable(ctx context.Context, clients *k8s.Clients, gvr schema.GroupVersionResource, ns string, req getRequest, getByName bool, listOpts metav1.ListOptions, out *fetchedResource) error {
	tables, err := k8s.NewTables(clients.REST, gvr)
	if err != nil {
		return err
	}
	var colIdx, wideIdx []int
	addPage := func(t *k8s.Table) {
		if colIdx == nil && wideIdx == nil {
			for i, c := range t.Columns {
				if c.Wide {
					wideIdx = append(wideIdx, i)
					out.wideColumns = append(out.wideColumns, c.Name)
				} else {
					colIdx = append(colIdx, i)
					out.columns = append(out.columns, c.Name)
				}
			}
		}
		for _, r := range t.Rows {
			out.count++
			row := make([]string, 0, len(colIdx)+1)
			if out.namespaced {
				row = append(row, r.Namespace)
			}
			for _, i := range colIdx {
				row = append(row, cell(r.Cells, i))
			}
			out.rows = append(out.rows, row)
			if req.format == output.FormatWide {
				wide := make([]string, 0, len(wideIdx))
				for _, i := range wideIdx {
					wide = append(wide, cell(r.Cells, i))
				}
				out.wide = append(out.wide, wide)
			}
		}
	}

	if getByName {
		t, err := tables.Get(ctx, ns, req.name)
		// Absent from some clusters is normal for a fleet, not a failure.
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			if errors.Is(err, k8s.ErrNoTable) {
				return err
			}
			return fmt.Errorf("get %s/%s: %w", gvr.Resource, req.name, err)
		}
		addPage(t)
		return nil
	}
	err = tables.List(ctx, ns, listOpts, func(t *k8s.Table) error {
		addPage(t)
		return nil
	})
	if err != nil && !errors.Is(err, k8s.ErrNoTable) {
		return fmt.Errorf("list %s: %w", gvr.Resource, err)
	}
	return err
}

func cell(cells []string, i int) string {
	if i < len(cells) {
		return cells[i]
	}
	return ""
}

// fetchObjects fills out from full objects: compact JSON for -o json|yaml,
// or rows from the built-in printers.
func fetchObjects(ctx context.Context, clients *k8s.Clients, gvr schema.GroupVersionResource, ctxName, ns string, req getRequest, getByName bool, listOpts metav1.ListOptions, out *fetchedResource) error {
	s := schemaFor(gvr.GroupResource())
	out.columns, out.wideColumns = s.Headers, s.WideHeaders
	add := func(u *unstructured.Unstructured) error {
		out.count++
		if req.format.Structured() {
			annotateContext(u, ctxName)
			b, err := json.Marshal(u.Object)
			if err != nil {
				return fmt.Errorf("encode %s/%s: %w", gvr.Resource, u.GetName(), err)
			}
			out.encoded = append(out.encoded, b)
			return nil
		}
		cols, wide := s.Row(u)
		if out.namespaced {
			cols = append([]string{u.GetNamespace()}, cols...)
		}
		out.rows = append(out.rows, cols)
		if req.format == output.FormatWide {
			out.wide = append(out.wide, wide)
		}
		return nil
	}

	if getByName {
		u, err := clients.Dynamic.Resource(gvr).Namespace(ns).Get(ctx, req.name, metav1.GetOptions{})
		// Absent from some clusters is normal for a fleet, not a failure.
		if apierrors.IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get %s/%s: %w", gvr.Resource, req.name, err)
		}
		return add(u)
	}
	// Page through the list as kubectl does (500 per request) so large
	// clusters are not asked to serialise every object in one response.
	lister := pager.New(func(ctx context.Context, opts metav1.ListOptions) (runtime.Object, error) {
		return clients.Dynamic.Resource(gvr).Namespace(ns).List(ctx, opts)
	})
	err := lister.EachListItem(ctx, listOpts, func(obj runtime.Object) error {
		u, ok := obj.(*unstructured.Unstructured)
		if !ok {
			return fmt.Errorf("unexpected list item %T", obj)
		}
		return add(u)
	})
	if err != nil {
		return fmt.Errorf("list %s: %w", gvr.Resource, err)
	}
	return nil
}
