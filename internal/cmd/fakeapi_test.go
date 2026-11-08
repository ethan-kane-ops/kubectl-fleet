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
// /version, and pod list/get. Pod lists page by the request's limit so the
// pager path is exercised. Every request path is recorded.
type fakeCluster struct {
	pods  []fakePod
	delay time.Duration

	mu       sync.Mutex
	requests []string
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

func (f *fakeCluster) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.requests = append(f.requests, r.URL.Path+"?"+r.URL.RawQuery)
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
		writeJSON(w, map[string]any{"kind": "APIGroupList", "apiVersion": "v1", "groups": []any{}})
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
			f.getPod(w, parts[0], parts[2])
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeCluster) listPods(w http.ResponseWriter, r *http.Request, ns string) {
	var match []fakePod
	for _, p := range f.pods {
		if ns == "" || p.namespace == ns {
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
	items := make([]any, 0, end-start)
	for _, p := range match[start:end] {
		items = append(items, podJSON(p))
	}
	writeJSON(w, map[string]any{"kind": "PodList", "apiVersion": "v1", "metadata": meta, "items": items})
}

func (f *fakeCluster) getPod(w http.ResponseWriter, ns, name string) {
	for _, p := range f.pods {
		if p.namespace == ns && p.name == name {
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
