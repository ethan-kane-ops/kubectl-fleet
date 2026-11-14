package report

import (
	_ "embed"
	"fmt"
	"html/template"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/inventory"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/output"
)

// FormatText is the report's plain-text form: aligned, ASCII only, pasteable
// into a chat message.
const FormatText output.Format = "text"

// Formats lists what Render accepts, for help and errors.
const Formats = "text|markdown|html|json|yaml"

// Render writes r in format f.
func Render(w io.Writer, r Report, f output.Format) error {
	switch f {
	case output.FormatJSON, output.FormatYAML:
		return output.Encode(w, r, f)
	case FormatText:
		return renderText(w, newView(r))
	case output.FormatMarkdown:
		return renderMarkdown(w, newView(r))
	case output.FormatHTML:
		return renderHTML(w, newView(r))
	}
	return fmt.Errorf("report output must be one of %s, not %q", Formats, f)
}

// labels is how each attention kind is shown, and its HTML class.
var labels = map[Kind][2]string{
	KindFailed:       {"FAILED", "bad"},
	KindUninspected:  {"NO ACCESS", "bad"},
	KindNotReady:     {"NOT READY", "bad"},
	KindCrashLoop:    {"CRASHLOOP", "bad"},
	KindStuckPending: {"PENDING", "warn"},
	KindVersion:      {"VERSION", "warn"},
	KindPartial:      {"INFO", "info"},
}

type viewItem struct {
	Label, Class, Context, Message string
}

// view is the report laid out for people, shared by every human format.
type view struct {
	Generated      string
	Selection      string
	Clusters       string
	Clear          bool
	Verdict        string
	Items          []viewItem
	Overview       [][2]string
	ClusterHeaders []string
	ClusterRows    [][]string
	CompHeaders    []string
	CompRows       [][]string
}

func newView(r Report) view {
	v := view{
		Generated:      r.GeneratedAt.UTC().Format("2006-01-02 15:04 UTC"),
		Selection:      selectionText(r.Selection),
		Clusters:       fmt.Sprintf("%d clusters (%d reachable)", r.Summary.Clusters, r.Summary.Reachable),
		ClusterHeaders: []string{"CONTEXT", "VERSION", "KUBELETS", "NODES", "ZONES", "PODS", "PENDING", "CRASHLOOP", "PROVISIONERS"},
		CompHeaders:    []string{"COMPONENT", "VERSION", "CLUSTERS"},
	}
	n := 0
	for _, i := range r.Attention {
		if i.Counts() {
			n++
		}
		l := labels[i.Kind]
		v.Items = append(v.Items, viewItem{Label: l[0], Class: l[1], Context: i.Context, Message: i.Message})
	}
	switch n {
	case 0:
		v.Clear, v.Verdict = true, "All clear"
	case 1:
		v.Verdict = "1 item needs attention"
	default:
		v.Verdict = fmt.Sprintf("%d items need attention", n)
	}

	s := r.Summary
	v.Overview = [][2]string{
		{"Nodes", fmt.Sprintf("%d of %d ready", s.NodesReady, s.NodesTotal)},
		{"Pods", fmt.Sprintf("%d running, %d pending (%d stuck), %d crashlooping, %d failed, %d total",
			s.PodsRunning, s.PodsPending, s.PodsStuckPending, s.PodsCrashLoop, s.PodsFailed, s.PodsTotal)},
		{"Control planes (clusters)", counts(s.ControlPlanes)},
		{"Kubelets (nodes)", counts(s.Kubelets)},
		{"Zones (nodes)", counts(s.Zones)},
		{"Instance types (nodes)", counts(s.InstanceTypes)},
		{"Provisioners (nodes)", provisioners(s.Provisioners)},
	}

	for _, c := range r.Clusters {
		if c.Cluster == nil {
			v.ClusterRows = append(v.ClusterRows, []string{c.Context, "-", "-", "-", "-", "-", "-", "-", "failed: " + c.Error})
			continue
		}
		row := []string{
			c.Context,
			c.ControlPlane,
			KubeletRange(c.Kubelets),
			fmt.Sprintf("%d/%d", c.NodesReady, c.NodesTotal),
			"-",
			fmt.Sprintf("%d", c.PodsRunning),
			fmt.Sprintf("%d", c.PodsPending),
			fmt.Sprintf("%d", c.PodsCrashLoop),
			provisionerKinds(c.Provisioners),
		}
		if len(c.Zones) > 0 {
			row[4] = fmt.Sprintf("%d", len(c.Zones))
		}
		// A list the cluster refused shows as unknown, not as zero: zero
		// would read as an empty or healthy cluster.
		if c.NodesForbidden {
			row[2], row[3], row[4], row[8] = "-", "-", "-", "-"
		}
		if c.PodsForbidden {
			row[5], row[6], row[7] = "-", "-", "-"
		}
		v.ClusterRows = append(v.ClusterRows, row)
	}
	for _, row := range r.Components {
		v.CompRows = append(v.CompRows, []string{row.Component, row.Version, fmt.Sprintf("%d", row.Clusters)})
	}
	return v
}

func selectionText(s Selection) string {
	var parts []string
	switch {
	case s.Context != "":
		parts = append(parts, "context "+s.Context)
	case len(s.Contexts) > 0:
		parts = append(parts, "contexts "+strings.Join(s.Contexts, ", "))
	default:
		parts = append(parts, "all contexts")
	}
	if s.Exclude != "" {
		parts = append(parts, "excluding "+s.Exclude)
	}
	if len(s.Where) > 0 {
		parts = append(parts, "where "+strings.Join(s.Where, ", "))
	}
	if s.ExpectVersion != "" {
		parts = append(parts, "expecting version "+s.ExpectVersion)
	}
	parts = append(parts, "pending counts after "+s.PendingFor)
	return strings.Join(parts, "; ")
}

func counts(cs []inventory.Count) string {
	if len(cs) == 0 {
		return "-"
	}
	parts := make([]string, len(cs))
	for i, c := range cs {
		parts[i] = fmt.Sprintf("%s (%d)", c.Value, c.N)
	}
	return strings.Join(parts, ", ")
}

func provisioners(ps []Provisioner) string {
	if len(ps) == 0 {
		return "-"
	}
	parts := make([]string, len(ps))
	for i, p := range ps {
		name := p.Kind
		if p.Name != "" {
			name += "/" + p.Name
		}
		parts[i] = fmt.Sprintf("%s (%d)", name, p.Nodes)
	}
	return strings.Join(parts, ", ")
}

// provisionerKinds sums a cluster's nodes by provisioner kind: "karpenter 3,
// nodegroup 1".
func provisionerKinds(ps []Provisioner) string {
	byKind := map[string]int{}
	for _, p := range ps {
		byKind[p.Kind] += p.Nodes
	}
	if len(byKind) == 0 {
		return "-"
	}
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Slice(kinds, func(i, j int) bool {
		if byKind[kinds[i]] != byKind[kinds[j]] {
			return byKind[kinds[i]] > byKind[kinds[j]]
		}
		return kinds[i] < kinds[j]
	})
	parts := make([]string, len(kinds))
	for i, k := range kinds {
		parts[i] = fmt.Sprintf("%s %d", k, byKind[k])
	}
	return strings.Join(parts, ", ")
}

func renderText(w io.Writer, v view) error {
	var b strings.Builder
	fmt.Fprintf(&b, "FLEET REPORT  %s\n", v.Generated)
	fmt.Fprintf(&b, "Selection: %s\n", v.Selection)
	fmt.Fprintf(&b, "%s\n\n", v.Clusters)

	fmt.Fprintf(&b, "%s\n", strings.ToUpper(v.Verdict))
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, i := range v.Items {
		_, _ = fmt.Fprintf(tw, "  [%s]\t%s\t%s\n", i.Label, i.Context, i.Message)
	}
	_ = tw.Flush()

	b.WriteString("\nFLEET OVERVIEW\n")
	tw = tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	for _, kv := range v.Overview {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\n", kv[0], kv[1])
	}
	_ = tw.Flush()

	b.WriteString("\nCLUSTERS\n")
	textTable(&b, v.ClusterHeaders, v.ClusterRows)
	if len(v.CompRows) > 0 {
		b.WriteString("\nKUBE-SYSTEM COMPONENTS\n")
		textTable(&b, v.CompHeaders, v.CompRows)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func textTable(b *strings.Builder, headers []string, rows [][]string) {
	tw := tabwriter.NewWriter(b, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "  %s\n", strings.Join(headers, "\t"))
	for _, r := range rows {
		_, _ = fmt.Fprintf(tw, "  %s\n", strings.Join(r, "\t"))
	}
	_ = tw.Flush()
}

func renderMarkdown(w io.Writer, v view) error {
	var b strings.Builder
	b.WriteString("# Fleet report\n\n")
	fmt.Fprintf(&b, "Generated %s. %s.\n\n", v.Generated, v.Clusters)
	fmt.Fprintf(&b, "Selection: %s.\n\n", mdText(v.Selection))

	fmt.Fprintf(&b, "## %s\n\n", v.Verdict)
	for _, i := range v.Items {
		fmt.Fprintf(&b, "- **%s** `%s`: %s\n", i.Label, strings.ReplaceAll(i.Context, "`", "'"), mdText(i.Message))
	}
	if len(v.Items) > 0 {
		b.WriteString("\n")
	}

	b.WriteString("## Fleet overview\n\n")
	overview := make([][]string, len(v.Overview))
	for i, kv := range v.Overview {
		overview[i] = []string{kv[0], kv[1]}
	}
	mdTable(&b, []string{"Metric", "Value"}, overview)

	b.WriteString("\n## Clusters\n\n")
	mdTable(&b, v.ClusterHeaders, v.ClusterRows)
	if len(v.CompRows) > 0 {
		b.WriteString("\n## kube-system components\n\n")
		mdTable(&b, v.CompHeaders, v.CompRows)
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// mdText escapes the characters that would turn plain text into Markdown
// markup.
func mdText(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "*", `\*`, "_", `\_`, "`", "\\`", "<", "&lt;", ">", "&gt;", "|", `\|`)
	return r.Replace(s)
}

func mdTable(b *strings.Builder, headers []string, rows [][]string) {
	t := &output.Table{Headers: headers}
	for _, r := range rows {
		t.Append(r, nil)
	}
	_ = output.Print(b, t, output.FormatMarkdown)
}

//go:embed report.html.tmpl
var htmlSource string

var htmlReport = template.Must(template.New("report").Parse(htmlSource))

func renderHTML(w io.Writer, v view) error {
	if err := htmlReport.Execute(w, struct {
		view
		CSS template.CSS
	}{v, template.CSS(output.Stylesheet)}); err != nil {
		return fmt.Errorf("render html: %w", err)
	}
	return nil
}
