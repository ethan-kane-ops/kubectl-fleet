package cmd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeCluster is a minimal API server: discovery for core/v1 pods and nodes,
// /version, and pod list/get. Pod lists honour a metadata.name field
// selector and page by the request's limit so the pager path is exercised.
// widgetGroup, when set, adds a cluster-scoped "widgets" resource (always
// empty) in that API group. Every request path is recorded.
type fakeCluster struct {
	pods        []fakePod
	delay       time.Duration
	widgetGroup string
	// Pods are served as a server-side Table when the client asks for one,
	// unless noTables is set (as for an API that cannot render tables).
	// extraColumn adds a column with that name, cell value "x", before Age.
	noTables    bool
	extraColumn string

	mu       sync.Mutex
	requests []string
	accept   map[string]string // last Accept header per path
}

type fakePod struct{ namespace, name string }

func (f *fakeCluster) start(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return srv.URL
}

func (f *fakeCluster) paths() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.requests...)
}

func (f *fakeCluster) acceptFor(path string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.accept[path]
}

func (f *fakeCluster) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.Path+"?"+r.URL.RawQuery)
	if f.accept == nil {
		f.accept = map[string]string{}
	}
	f.accept[r.URL.Path] = r.Header.Get("Accept")
	f.mu.Unlock()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	w.Header().Set("Content-Type", "application/json")
	p := r.URL.Path
	switch {
	case p == "/version":
		writeJSON(w, map[string]string{"gitVersion": "v1.31.0"})
	case p == "/api":
		writeJSON(w, map[string]any{"kind": "APIVersions", "versions": []string{"v1"}})
	case p == "/apis":
		groups := []any{}
		if f.widgetGroup != "" {
			gv := map[string]any{"groupVersion": f.widgetGroup + "/v1", "version": "v1"}
			groups = append(groups, map[string]any{"name": f.widgetGroup, "versions": []any{gv}, "preferredVersion": gv})
		}
		writeJSON(w, map[string]any{"kind": "APIGroupList", "apiVersion": "v1", "groups": groups})
	case f.widgetGroup != "" && p == "/apis/"+f.widgetGroup+"/v1":
		writeJSON(w, map[string]any{
			"kind": "APIResourceList", "groupVersion": f.widgetGroup + "/v1",
			"resources": []map[string]any{{"name": "widgets", "singularName": "widget", "namespaced": false, "kind": "Widget", "verbs": []string{"get", "list"}}},
		})
	case f.widgetGroup != "" && p == "/apis/"+f.widgetGroup+"/v1/widgets":
		writeJSON(w, map[string]any{"kind": "WidgetList", "apiVersion": f.widgetGroup + "/v1", "metadata": map[string]any{}, "items": []any{}})
	case p == "/api/v1":
		writeJSON(w, map[string]any{
			"kind": "APIResourceList", "groupVersion": "v1",
			"resources": []map[string]any{
				{"name": "pods", "singularName": "pod", "namespaced": true, "kind": "Pod", "shortNames": []string{"po"}, "verbs": []string{"get", "list"}},
				{"name": "nodes", "singularName": "node", "namespaced": false, "kind": "Node", "verbs": []string{"get", "list"}},
			},
		})
	case p == "/api/v1/nodes":
		writeJSON(w, map[string]any{"kind": "NodeList", "apiVersion": "v1", "metadata": map[string]any{}, "items": []any{}})
	case p == "/api/v1/pods":
		f.listPods(w, r, "")
	case strings.HasPrefix(p, "/api/v1/namespaces/"):
		parts := strings.Split(strings.TrimPrefix(p, "/api/v1/namespaces/"), "/")
		switch {
		case len(parts) == 2 && parts[1] == "pods":
			f.listPods(w, r, parts[0])
		case len(parts) == 3 && parts[1] == "pods":
			f.getPod(w, r, parts[0], parts[2])
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeCluster) listPods(w http.ResponseWriter, r *http.Request, ns string) {
	var match []fakePod
	wantName := strings.TrimPrefix(r.URL.Query().Get("fieldSelector"), "metadata.name=")
	for _, p := range f.pods {
		if (ns == "" || p.namespace == ns) && (wantName == "" || p.name == wantName) {
			match = append(match, p)
		}
	}
	start, _ := strconv.Atoi(r.URL.Query().Get("continue"))
	end := len(match)
	if limit, _ := strconv.Atoi(r.URL.Query().Get("limit")); limit > 0 && start+limit < end {
		end = start + limit
	}
	meta := map[string]any{}
	if end < len(match) {
		meta["continue"] = strconv.Itoa(end)
	}
	if f.wantsTable(r) {
		writeJSON(w, f.podTable(meta, match[start:end]))
		return
	}
	items := make([]any, 0, end-start)
	for _, p := range match[start:end] {
		items = append(items, podJSON(p))
	}
	writeJSON(w, map[string]any{"kind": "PodList", "apiVersion": "v1", "metadata": meta, "items": items})
}

func (f *fakeCluster) wantsTable(r *http.Request) bool {
	return !f.noTables && strings.Contains(r.Header.Get("Accept"), "as=Table")
}

// podTable renders pods the way an API server's table convertor does.
func (f *fakeCluster) podTable(meta map[string]any, pods []fakePod) map[string]any {
	cols := []map[string]any{{"name": "Name", "priority": 0}, {"name": "Ready", "priority": 0}, {"name": "Status", "priority": 0}, {"name": "Restarts", "priority": 0}}
	if f.extraColumn != "" {
		cols = append(cols, map[string]any{"name": f.extraColumn, "priority": 0})
	}
	cols = append(cols, map[string]any{"name": "Age", "priority": 0}, map[string]any{"name": "IP", "priority": 1}, map[string]any{"name": "Node", "priority": 1})
	rows := make([]any, 0, len(pods))
	for _, p := range pods {
		cells := []any{p.name, "1/1", "Running", 3}
		if f.extraColumn != "" {
			cells = append(cells, "x")
		}
		cells = append(cells, "1d", "10.0.0.9", nil)
		rows = append(rows, map[string]any{
			"cells":  cells,
			"object": map[string]any{"kind": "PartialObjectMetadata", "apiVersion": "meta.k8s.io/v1", "metadata": map[string]any{"name": p.name, "namespace": p.namespace}},
		})
	}
	return map[string]any{"kind": "Table", "apiVersion": "meta.k8s.io/v1", "metadata": meta, "columnDefinitions": cols, "rows": rows}
}

func (f *fakeCluster) getPod(w http.ResponseWriter, r *http.Request, ns, name string) {
	for _, p := range f.pods {
		if p.namespace == ns && p.name == name {
			if f.wantsTable(r) {
				writeJSON(w, f.podTable(map[string]any{}, []fakePod{p}))
				return
			}
			writeJSON(w, podJSON(p))
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
	writeJSON(w, map[string]any{
		"kind": "Status", "apiVersion": "v1", "status": "Failure", "reason": "NotFound", "code": 404,
		"message":  fmt.Sprintf("pods %q not found", name),
		"metadata": map[string]any{},
		"details":  map[string]any{"name": name, "kind": "pods"},
	})
}

func podJSON(p fakePod) map[string]any {
	return map[string]any{
		"kind": "Pod", "apiVersion": "v1",
		"metadata": map[string]any{"name": p.name, "namespace": p.namespace, "creationTimestamp": "2026-01-01T00:00:00Z"},
		"status":   map[string]any{"phase": "Running"},
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	_ = json.NewEncoder(w).Encode(v)
}

// fakeContext is one kubeconfig context pointing at a fake server.
type fakeContext struct {
	name, server, namespace string
}

// writeFakeKubeconfig writes a kubeconfig with one cluster per context and
// returns its path.
func writeFakeKubeconfig(t *testing.T, ctxs ...fakeContext) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("apiVersion: v1\nkind: Config\nusers:\n- name: u\n  user: {token: t}\nclusters:\n")
	for _, c := range ctxs {
		fmt.Fprintf(&b, "- name: %s\n  cluster: {server: %q}\n", c.name, c.server)
	}
	b.WriteString("contexts:\n")
	for _, c := range ctxs {
		fmt.Fprintf(&b, "- name: %s\n  context: {cluster: %s, user: u, namespace: %q}\n", c.name, c.name, c.namespace)
	}
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}
