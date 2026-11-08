//go:build !windows

// Command loadtest runs a kubectl-fleet binary against fake fleets served
// in-process and fails when a scenario exceeds its peak-memory budget or
// prints the wrong number of rows. It covers the two ways a fleet gets
// large: a few clusters with many objects, and many contexts behind a large
// kubeconfig.
//
//	go run ./hack/loadtest -bin bin/kubectl-fleet
//
// Wall time is printed for comparison between runs but not enforced: it
// depends too much on the machine. Peak RSS is stable enough to gate on.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type fleetShape struct {
	name     string
	clusters int
	pods     int
	nodes    int
	// padUsers adds unused kubeconfig users with embedded credential data,
	// so the file is the size real fleets carry (~5 KB per entry).
	padUsers int
}

type scenario struct {
	args     []string
	budgetMB int
	// wantLines is the expected stdout line count, 0 to skip the check.
	wantLines func(f fleetShape) int
}

var suites = []struct {
	shape     fleetShape
	scenarios []scenario
}{
	{
		shape: fleetShape{name: "big clusters", clusters: 20, pods: 10000, nodes: 200},
		scenarios: []scenario{
			{args: []string{"get", "pods", "-A"}, budgetMB: 400, wantLines: podRows},
			{args: []string{"get", "pods", "-A", "--server-print=false"}, budgetMB: 600, wantLines: podRows},
			{args: []string{"get", "pods", "-A", "-o", "name"}, budgetMB: 400, wantLines: podNames},
			{args: []string{"get", "pods", "-A", "-o", "json"}, budgetMB: 1500},
			{args: []string{"status"}, budgetMB: 400, wantLines: contextRows},
		},
	},
	{
		shape: fleetShape{name: "many contexts", clusters: 300, pods: 200, nodes: 10, padUsers: 300},
		scenarios: []scenario{
			// Tight budgets: these hold almost nothing per context, so
			// growth here means per-context overhead (such as re-parsing the
			// kubeconfig for each context) has crept back in.
			{args: []string{"contexts", "--check"}, budgetMB: 100, wantLines: contextRows},
			{args: []string{"version"}, budgetMB: 100, wantLines: func(f fleetShape) int { return f.clusters + 2 }},
			{args: []string{"status"}, budgetMB: 300, wantLines: contextRows},
			{args: []string{"get", "pods", "-A"}, budgetMB: 250, wantLines: podRows},
		},
	},
}

func podRows(f fleetShape) int     { return f.clusters*f.pods + 1 }
func podNames(f fleetShape) int    { return f.clusters * f.pods }
func contextRows(f fleetShape) int { return f.clusters + 1 }

func main() {
	bin := flag.String("bin", "bin/kubectl-fleet", "kubectl-fleet binary to test")
	scale := flag.Float64("scale", 1, "multiply pods per cluster (and budgets stay fixed): >1 to stress, <1 for a quick run")
	latency := flag.Duration("latency", 20*time.Millisecond, "added latency per API request")
	flag.Parse()

	work, err := os.MkdirTemp("", "kubectl-fleet-loadtest-")
	if err != nil {
		fatal(err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	failed := false
	fmt.Printf("%-14s %-36s %8s %9s %8s  %s\n", "FLEET", "COMMAND", "WALL", "PEAK", "BUDGET", "RESULT")
	for _, s := range suites {
		shape := s.shape
		shape.pods = max(1, int(float64(shape.pods)**scale))
		kubeconfig, stop, err := serveFleet(shape, *latency, work)
		if err != nil {
			fatal(err)
		}
		for _, sc := range s.scenarios {
			res := run(*bin, sc, shape, kubeconfig, filepath.Join(work, "cache"))
			if res != "ok" {
				failed = true
			}
		}
		stop()
	}
	if failed {
		os.Exit(1)
	}
}

func run(bin string, sc scenario, shape fleetShape, kubeconfig, cacheDir string) string {
	args := append(append([]string{}, sc.args...), "--kubeconfig", kubeconfig, "--cache-dir", cacheDir)
	cmd := exec.Command(bin, args...)
	var stdout lineCounter
	var stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	err := cmd.Run()
	wall := time.Since(start)

	peakMB := 0
	if cmd.ProcessState != nil {
		if ru, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage); ok {
			peakMB = int(maxRSSBytes(ru) / (1 << 20))
		}
	}
	result := "ok"
	switch {
	case err != nil:
		result = fmt.Sprintf("FAIL: %v: %s", err, firstLine(stderr.String()))
	case peakMB > sc.budgetMB:
		result = fmt.Sprintf("FAIL: peak %d MB over budget", peakMB)
	case sc.wantLines != nil && stdout.lines != sc.wantLines(shape):
		result = fmt.Sprintf("FAIL: %d lines, want %d", stdout.lines, sc.wantLines(shape))
	}
	fmt.Printf("%-14s %-36s %7.2fs %6d MB %5d MB  %s\n", shape.name, strings.Join(sc.args, " "), wall.Seconds(), peakMB, sc.budgetMB, result)
	return result
}

// maxRSSBytes normalises ru_maxrss: bytes on macOS, kilobytes on Linux.
func maxRSSBytes(ru *syscall.Rusage) int64 {
	if runtime.GOOS == "darwin" {
		return int64(ru.Maxrss)
	}
	return int64(ru.Maxrss) * 1024
}

type lineCounter struct{ lines int }

func (c *lineCounter) Write(p []byte) (int, error) {
	c.lines += bytes.Count(p, []byte("\n"))
	return len(p), nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "loadtest:", err)
	os.Exit(2)
}

// serveFleet starts one fake API server per cluster and writes a kubeconfig
// with one context per server. stop closes the servers.
func serveFleet(f fleetShape, latency time.Duration, dir string) (kubeconfig string, stop func(), err error) {
	pods := make([][]byte, f.pods)
	for i := range pods {
		if pods[i], err = json.Marshal(podObject(i, f.nodes)); err != nil {
			return "", nil, err
		}
	}
	nodes := make([][]byte, f.nodes)
	for i := range nodes {
		if nodes[i], err = json.Marshal(nodeObject(i)); err != nil {
			return "", nil, err
		}
	}
	rows := make([][]byte, f.pods)
	for i := range rows {
		if rows[i], err = json.Marshal(podTableRow(i, f.nodes)); err != nil {
			return "", nil, err
		}
	}
	h := &fakeAPI{pods: pods, podRows: rows, nodes: nodes, latency: latency}

	var cfg, ctxs strings.Builder
	cfg.WriteString("apiVersion: v1\nkind: Config\nusers:\n- name: u\n  user: {token: t}\n")
	pad := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xa5}, 3000))
	for i := range f.padUsers {
		fmt.Fprintf(&cfg, "- name: pad-%d\n  user: {client-key-data: %s, client-certificate-data: %s}\n", i, pad, pad[:1500])
	}
	cfg.WriteString("clusters:\n")
	var servers []*http.Server
	for i := range f.clusters {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return "", nil, err
		}
		srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
		servers = append(servers, srv)
		go func() { _ = srv.Serve(ln) }()
		fmt.Fprintf(&cfg, "- name: c%03d\n  cluster: {server: \"http://%s\"}\n", i, ln.Addr())
		fmt.Fprintf(&ctxs, "- name: ctx-%03d\n  context: {cluster: c%03d, user: u}\n", i, i)
	}
	cfg.WriteString("contexts:\n" + ctxs.String())
	kubeconfig = filepath.Join(dir, strings.ReplaceAll(f.name, " ", "-")+".kubeconfig")
	if err := os.WriteFile(kubeconfig, []byte(cfg.String()), 0o600); err != nil {
		return "", nil, err
	}
	return kubeconfig, func() {
		for _, s := range servers {
			_ = s.Close()
		}
	}, nil
}

// fakeAPI serves discovery, /version and paged pod and node lists, and pod
// lists as server-side tables when asked. Every cluster serves the same
// pre-encoded objects.
type fakeAPI struct {
	pods, podRows, nodes [][]byte
	latency              time.Duration
}

func (a *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	time.Sleep(a.latency)
	w.Header().Set("Content-Type", "application/json")
	enc := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	switch r.URL.Path {
	case "/version":
		enc(map[string]string{"gitVersion": "v1.31.0"})
	case "/api":
		enc(map[string]any{"kind": "APIVersions", "versions": []string{"v1"}})
	case "/apis":
		enc(map[string]any{"kind": "APIGroupList", "apiVersion": "v1", "groups": []any{}})
	case "/api/v1":
		enc(map[string]any{"kind": "APIResourceList", "groupVersion": "v1", "resources": []map[string]any{
			{"name": "pods", "singularName": "pod", "namespaced": true, "kind": "Pod", "shortNames": []string{"po"}, "verbs": []string{"get", "list"}},
			{"name": "nodes", "singularName": "node", "namespaced": false, "kind": "Node", "verbs": []string{"get", "list"}},
		}})
	case "/api/v1/pods":
		if strings.Contains(r.Header.Get("Accept"), "as=Table") {
			page(w, r, "Table", a.podRows)
			return
		}
		page(w, r, "PodList", a.pods)
	case "/api/v1/nodes":
		page(w, r, "NodeList", a.nodes)
	default:
		http.NotFound(w, r)
	}
}

// page writes one page of items, honouring limit and an index continue
// token. A Table carries its column definitions and puts items in rows.
func page(w http.ResponseWriter, r *http.Request, kind string, items [][]byte) {
	start, _ := strconv.Atoi(r.URL.Query().Get("continue"))
	start = min(start, len(items))
	end := len(items)
	if limit, _ := strconv.Atoi(r.URL.Query().Get("limit")); limit > 0 && start+limit < end {
		end = start + limit
	}
	cont := ""
	if end < len(items) {
		cont = strconv.Itoa(end)
	}
	if kind == "Table" {
		_, _ = fmt.Fprintf(w, `{"kind":"Table","apiVersion":"meta.k8s.io/v1","metadata":{"continue":%q},"columnDefinitions":%s,"rows":[`, cont, podColumns)
	} else {
		_, _ = fmt.Fprintf(w, `{"kind":%q,"apiVersion":"v1","metadata":{"continue":%q},"items":[`, kind, cont)
	}
	for i, b := range items[start:end] {
		if i > 0 {
			_, _ = w.Write([]byte(","))
		}
		_, _ = w.Write(b)
	}
	_, _ = w.Write([]byte("]}"))
}

// podObject is a pod shaped like a typical Deployment replica (~2 KB JSON).
func podObject(i, nodes int) map[string]any {
	ns := fmt.Sprintf("team-%d", i%50)
	env := make([]any, 0, 15)
	for e := range 15 {
		env = append(env, map[string]any{"name": fmt.Sprintf("ENV_VAR_%d", e), "value": strings.Repeat("v", 30)})
	}
	running := map[string]any{"running": map[string]any{"startedAt": "2026-11-01T00:00:00Z"}}
	return map[string]any{
		"kind": "Pod", "apiVersion": "v1",
		"metadata": map[string]any{
			"name": fmt.Sprintf("app-%d-7d4f6c5b9-x%05d", i%200, i), "namespace": ns, "uid": fmt.Sprintf("uid-%d", i),
			"creationTimestamp": "2026-11-01T00:00:00Z", "resourceVersion": strconv.Itoa(1000 + i),
			"labels":          map[string]any{"app": fmt.Sprintf("app-%d", i%200), "pod-template-hash": "7d4f6c5b9", "team": ns},
			"annotations":     map[string]any{"kubectl.kubernetes.io/restartedAt": "2026-11-01T00:00:00Z", "prometheus.io/scrape": "true"},
			"ownerReferences": []any{map[string]any{"apiVersion": "apps/v1", "kind": "ReplicaSet", "name": "rs", "uid": "u", "controller": true}},
		},
		"spec": map[string]any{
			"nodeName": fmt.Sprintf("node-%d", i%max(nodes, 1)),
			"containers": []any{
				map[string]any{"name": "app", "image": "registry.example.com/team/app:v1.2.3", "env": env,
					"resources": map[string]any{"requests": map[string]any{"cpu": "100m", "memory": "128Mi"}}},
				map[string]any{"name": "sidecar", "image": "registry.example.com/proxy:v9"},
			},
		},
		"status": map[string]any{
			"phase": "Running", "podIP": "10.0.0.1", "hostIP": "192.168.0.1",
			"conditions": []any{
				map[string]any{"type": "Ready", "status": "True"},
				map[string]any{"type": "ContainersReady", "status": "True"},
				map[string]any{"type": "PodScheduled", "status": "True"},
			},
			"containerStatuses": []any{
				map[string]any{"name": "app", "ready": true, "restartCount": 0, "started": true, "state": running},
				map[string]any{"name": "sidecar", "ready": true, "restartCount": 0, "started": true, "state": running},
			},
		},
	}
}

// podColumns are the API server's pod table columns.
const podColumns = `[{"name":"Name","type":"string","priority":0},{"name":"Ready","type":"string","priority":0},` +
	`{"name":"Status","type":"string","priority":0},{"name":"Restarts","type":"string","priority":0},` +
	`{"name":"Age","type":"string","priority":0},{"name":"IP","type":"string","priority":1},` +
	`{"name":"Node","type":"string","priority":1},{"name":"Nominated Node","type":"string","priority":1},` +
	`{"name":"Readiness Gates","type":"string","priority":1}]`

// podTableRow is podObject as a server table row with includeObject=Metadata.
// Real metadata also carries managedFields, which this leaves out.
func podTableRow(i, nodes int) map[string]any {
	obj := podObject(i, nodes)
	return map[string]any{
		"cells":  []any{obj["metadata"].(map[string]any)["name"], "2/2", "Running", "0", "7d", "10.0.0.1", fmt.Sprintf("node-%d", i%max(nodes, 1)), "<none>", "<none>"},
		"object": map[string]any{"kind": "PartialObjectMetadata", "apiVersion": "meta.k8s.io/v1", "metadata": obj["metadata"]},
	}
}

// nodeObject is a node carrying a typical status.images list (~5 KB JSON).
func nodeObject(i int) map[string]any {
	images := make([]any, 0, 40)
	for j := range 40 {
		images = append(images, map[string]any{"names": []any{fmt.Sprintf("registry.example.com/image-%d@sha256:%064d", j, j)}, "sizeBytes": 123456789})
	}
	return map[string]any{
		"kind": "Node", "apiVersion": "v1",
		"metadata": map[string]any{"name": fmt.Sprintf("node-%d", i), "creationTimestamp": "2026-11-01T00:00:00Z"},
		"status": map[string]any{
			"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
			"images":     images,
		},
	}
}
