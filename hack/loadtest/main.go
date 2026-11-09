//go:build !windows

// Command loadtest runs a kubectl-fleet binary against fake fleets served
// in-process and fails when a scenario exceeds its peak-memory budget,
// prints the wrong number of rows, or misses an expected stderr line. It
// covers the two ways a fleet gets large: a few clusters with many objects,
// and many contexts behind a large kubeconfig, some of them unreachable.
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

// fleetShape describes a fake fleet. Cluster i is "sick" when i%10 == 0:
// its last node is NotReady and 1 in 100 of its pods crashloops. Clusters
// with even i run v1.29.8, odd ones v1.30.4. The last unreachable contexts
// point at a closed port.
type fleetShape struct {
	name        string
	clusters    int
	unreachable int
	pods        int
	nodes       int
	// padUsers adds unused kubeconfig users with embedded credential data,
	// so the file is the size real fleets carry (~5 KB per entry).
	padUsers int
}

func (f fleetShape) reachable() int { return f.clusters - f.unreachable }

// sick counts reachable sick clusters.
func (f fleetShape) sick() int { return (f.reachable() + 9) / 10 }

// crashloopPods is how many pods crashloop in one sick cluster.
func (f fleetShape) crashloopPods() int { return (f.pods + 99) / 100 }

type scenario struct {
	args     []string
	budgetMB int
	// wantLines is the expected stdout line count; nil skips the check.
	wantLines func(f fleetShape) int
	// wantStderr is a fragment stderr must contain; nil skips the check.
	wantStderr func(f fleetShape) string
}

var (
	reachableRows = func(f fleetShape) int { return f.reachable() + 1 }
	podRows       = func(f fleetShape) int { return f.reachable()*f.pods + 1 }
	podNames      = func(f fleetShape) int { return f.reachable() * f.pods }
	problemPods   = func(f fleetShape) int { return f.sick()*f.crashloopPods() + 1 }
	failedSummary = func(f fleetShape) string {
		return fmt.Sprintf("%d of %d contexts failed: %d connection refused", f.unreachable, f.clusters, f.unreachable)
	}
)

var suites = []struct {
	shape     fleetShape
	scenarios []scenario
}{
	{
		shape: fleetShape{name: "big clusters", clusters: 20, pods: 10000, nodes: 200},
		scenarios: []scenario{
			{args: []string{"get", "pods", "-A"}, budgetMB: 400, wantLines: podRows},
			{args: []string{"get", "pods", "-A", "--server-print=false"}, budgetMB: 800, wantLines: podRows},
			{args: []string{"get", "pods", "-A", "-o", "name"}, budgetMB: 400, wantLines: podNames},
			{args: []string{"get", "pods", "-A", "-o", "json"}, budgetMB: 1500},
			{args: []string{"get", "pods", "-A", "--problems"}, budgetMB: 400, wantLines: problemPods},
			{args: []string{"status"}, budgetMB: 400, wantLines: reachableRows},
			{args: []string{"version", "--detailed"}, budgetMB: 400},
		},
	},
	{
		shape: fleetShape{name: "200 contexts", clusters: 200, unreachable: 20, pods: 200, nodes: 10, padUsers: 200},
		scenarios: []scenario{
			// Tight budgets: these hold almost nothing per context, so
			// growth here means per-context overhead (such as re-parsing the
			// kubeconfig for each context) has crept back in.
			{args: []string{"contexts", "--check"}, budgetMB: 100, wantLines: func(f fleetShape) int { return f.clusters + 1 }},
			{args: []string{"version"}, budgetMB: 100, wantLines: func(f fleetShape) int { return f.reachable() + 2 }, wantStderr: failedSummary},
			{args: []string{"status"}, budgetMB: 300, wantLines: reachableRows, wantStderr: failedSummary},
			{args: []string{"status", "--where", "reachable", "--where", "version<1.30"}, budgetMB: 300,
				wantLines:  func(f fleetShape) int { return (f.reachable()+1)/2 + 1 },
				wantStderr: func(f fleetShape) string { return fmt.Sprintf("skipped %d unreachable", f.unreachable) }},
			{args: []string{"status", "--where", "reachable", "--where", "nodes>=10"}, budgetMB: 300, wantLines: reachableRows},
			{args: []string{"status", "--problems"}, budgetMB: 300, wantLines: func(f fleetShape) int { return f.sick() + 1 }},
			{args: []string{"status", "--sort-by", "pods", "--top", "10"}, budgetMB: 300, wantLines: func(fleetShape) int { return 11 }},
			{args: []string{"status", "--exclude", "^ctx-0"}, budgetMB: 300, wantLines: func(f fleetShape) int { return f.reachable() - 100 + 1 }},
			{args: []string{"get", "pods", "-A"}, budgetMB: 250, wantLines: podRows, wantStderr: failedSummary},
			{args: []string{"get", "pods", "-A", "--problems"}, budgetMB: 250, wantLines: problemPods},
			{args: []string{"version", "--detailed"}, budgetMB: 300, wantStderr: failedSummary},
		},
	},
}

func main() {
	bin := flag.String("bin", "bin/kubectl-fleet", "kubectl-fleet binary to test")
	scale := flag.Float64("scale", 1, "multiply pods per cluster (budgets stay fixed): >1 to stress, <1 for a quick run")
	latency := flag.Duration("latency", 20*time.Millisecond, "added latency per API request")
	flag.Parse()

	work, err := os.MkdirTemp("", "kubectl-fleet-loadtest-")
	if err != nil {
		fatal(err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	failed := false
	fmt.Printf("%-14s %-48s %8s %9s %8s  %s\n", "FLEET", "COMMAND", "WALL", "PEAK", "BUDGET", "RESULT")
	for _, s := range suites {
		shape := s.shape
		shape.pods = max(1, int(float64(shape.pods)**scale))
		kubeconfig, stop, err := serveFleet(shape, *latency, work)
		if err != nil {
			fatal(err)
		}
		for _, sc := range s.scenarios {
			if run(*bin, sc, shape, kubeconfig, filepath.Join(work, "cache")) != "ok" {
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
	case sc.wantStderr != nil && !strings.Contains(stderr.String(), sc.wantStderr(shape)):
		result = fmt.Sprintf("FAIL: stderr lacks %q: %s", sc.wantStderr(shape), firstLine(stderr.String()))
	}
	fmt.Printf("%-14s %-48s %7.2fs %6d MB %5d MB  %s\n", shape.name, strings.Join(sc.args, " "), wall.Seconds(), peakMB, sc.budgetMB, result)
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

// clusterData is one pre-encoded variant of a cluster's objects; every
// healthy cluster shares one, every sick cluster the other.
type clusterData struct {
	pods, podRows, nodes, nodeMeta [][]byte
}

func encodeAll(n int, fn func(i int) any) ([][]byte, error) {
	out := make([][]byte, n)
	for i := range out {
		b, err := json.Marshal(fn(i))
		if err != nil {
			return nil, err
		}
		out[i] = b
	}
	return out, nil
}

func buildData(f fleetShape, sick bool) (*clusterData, error) {
	crash := func(i int) bool { return sick && i%100 == 0 }
	notReady := func(i int) bool { return sick && i == f.nodes-1 }
	var d clusterData
	var err error
	if d.pods, err = encodeAll(f.pods, func(i int) any { return podObject(i, f.nodes, crash(i)) }); err != nil {
		return nil, err
	}
	if d.podRows, err = encodeAll(f.pods, func(i int) any { return podTableRow(i, f.nodes, crash(i)) }); err != nil {
		return nil, err
	}
	if d.nodes, err = encodeAll(f.nodes, func(i int) any { return nodeObject(i, notReady(i)) }); err != nil {
		return nil, err
	}
	if d.nodeMeta, err = encodeAll(f.nodes, func(i int) any {
		return map[string]any{"kind": "PartialObjectMetadata", "apiVersion": "meta.k8s.io/v1",
			"metadata": map[string]any{"name": fmt.Sprintf("node-%d", i)}}
	}); err != nil {
		return nil, err
	}
	return &d, nil
}

// serveFleet starts one fake API server per reachable cluster and writes a
// kubeconfig with one context per cluster. stop closes the servers.
func serveFleet(f fleetShape, latency time.Duration, dir string) (kubeconfig string, stop func(), err error) {
	healthy, err := buildData(f, false)
	if err != nil {
		return "", nil, err
	}
	sick, err := buildData(f, true)
	if err != nil {
		return "", nil, err
	}

	var cfg, ctxs strings.Builder
	cfg.WriteString("apiVersion: v1\nkind: Config\nusers:\n- name: u\n  user: {token: t}\n")
	pad := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xa5}, 3000))
	for i := range f.padUsers {
		fmt.Fprintf(&cfg, "- name: pad-%d\n  user: {client-key-data: %s, client-certificate-data: %s}\n", i, pad, pad[:1500])
	}
	cfg.WriteString("clusters:\n")
	var servers []*http.Server
	for i := range f.clusters {
		addr := "127.0.0.1:1" // nothing listens: connection refused
		if i < f.reachable() {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return "", nil, err
			}
			api := &fakeAPI{data: healthy, latency: latency, version: "v1.30.4-eks-a737599"}
			if i%10 == 0 {
				api.data = sick
			}
			if i%2 == 0 {
				api.version = "v1.29.8-eks-a737599"
			}
			srv := &http.Server{Handler: api, ReadHeaderTimeout: 10 * time.Second}
			servers = append(servers, srv)
			go func() { _ = srv.Serve(ln) }()
			addr = ln.Addr().String()
		}
		fmt.Fprintf(&cfg, "- name: c%03d\n  cluster: {server: \"http://%s\"}\n", i, addr)
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

// fakeAPI serves discovery, /version, paged pod and node lists (pods also
// as server-side tables, nodes also metadata-only) and kube-system
// workloads.
type fakeAPI struct {
	data    *clusterData
	latency time.Duration
	version string
}

func (a *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	time.Sleep(a.latency)
	w.Header().Set("Content-Type", "application/json")
	enc := func(v any) { _ = json.NewEncoder(w).Encode(v) }
	accept := r.Header.Get("Accept")
	switch r.URL.Path {
	case "/version":
		enc(map[string]string{"gitVersion": a.version})
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
		if strings.Contains(accept, "as=Table") {
			page(w, r, "Table", a.data.podRows)
			return
		}
		page(w, r, "PodList", a.data.pods)
	case "/api/v1/nodes":
		if strings.Contains(accept, "as=PartialObjectMetadataList") {
			page(w, r, "PartialObjectMetadataList", a.data.nodeMeta)
			return
		}
		page(w, r, "NodeList", a.data.nodes)
	case "/apis/apps/v1/namespaces/kube-system/deployments":
		enc(workloads("DeploymentList", "Deployment", map[string]string{
			"coredns": "registry.k8s.io/coredns/coredns:v1.11.1", "metrics-server": "registry.k8s.io/metrics-server/metrics-server:v0.7.2"}))
	case "/apis/apps/v1/namespaces/kube-system/daemonsets":
		enc(workloads("DaemonSetList", "DaemonSet", map[string]string{
			"kube-proxy": "registry.k8s.io/kube-proxy:" + a.version, "aws-node": "602401143452.dkr.ecr.eu-west-1.amazonaws.com/amazon-k8s-cni:v1.18.3"}))
	default:
		http.NotFound(w, r)
	}
}

func workloads(listKind, kind string, images map[string]string) map[string]any {
	items := []any{}
	for name, image := range images {
		items = append(items, map[string]any{"kind": kind, "apiVersion": "apps/v1",
			"metadata": map[string]any{"name": name, "namespace": "kube-system"},
			"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
				"containers": []any{map[string]any{"name": name, "image": image}}}}}})
	}
	return map[string]any{"kind": listKind, "apiVersion": "apps/v1", "metadata": map[string]any{}, "items": items}
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
	switch kind {
	case "Table":
		_, _ = fmt.Fprintf(w, `{"kind":"Table","apiVersion":"meta.k8s.io/v1","metadata":{"continue":%q},"columnDefinitions":%s,"rows":[`, cont, podColumns)
	case "PartialObjectMetadataList":
		_, _ = fmt.Fprintf(w, `{"kind":%q,"apiVersion":"meta.k8s.io/v1","metadata":{"continue":%q},"items":[`, kind, cont)
	default:
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

// podColumns are the API server's pod table columns.
const podColumns = `[{"name":"Name","type":"string","priority":0},{"name":"Ready","type":"string","priority":0},` +
	`{"name":"Status","type":"string","priority":0},{"name":"Restarts","type":"string","priority":0},` +
	`{"name":"Age","type":"string","priority":0},{"name":"IP","type":"string","priority":1},` +
	`{"name":"Node","type":"string","priority":1},{"name":"Nominated Node","type":"string","priority":1},` +
	`{"name":"Readiness Gates","type":"string","priority":1}]`

// podObject is a pod shaped like a typical Deployment replica (~2 KB JSON).
// A crashlooping pod has its app container waiting in CrashLoopBackOff.
func podObject(i, nodes int, crashloop bool) map[string]any {
	ns := fmt.Sprintf("team-%d", i%50)
	env := make([]any, 0, 15)
	for e := range 15 {
		env = append(env, map[string]any{"name": fmt.Sprintf("ENV_VAR_%d", e), "value": strings.Repeat("v", 30)})
	}
	running := map[string]any{"running": map[string]any{"startedAt": "2026-11-01T00:00:00Z"}}
	app := map[string]any{"name": "app", "ready": true, "restartCount": 0, "started": true, "state": running}
	if crashloop {
		app = map[string]any{"name": "app", "ready": false, "restartCount": 12, "state": map[string]any{"waiting": map[string]any{"reason": "CrashLoopBackOff"}}}
	}
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
				app,
				map[string]any{"name": "sidecar", "ready": true, "restartCount": 0, "started": true, "state": running},
			},
		},
	}
}

// podTableRow is podObject as a server table row with includeObject=Metadata.
// Real metadata also carries managedFields, which this leaves out.
func podTableRow(i, nodes int, crashloop bool) map[string]any {
	obj := podObject(i, nodes, crashloop)
	ready, status, restarts := "2/2", "Running", "0"
	if crashloop {
		ready, status, restarts = "1/2", "CrashLoopBackOff", "12"
	}
	return map[string]any{
		"cells":  []any{obj["metadata"].(map[string]any)["name"], ready, status, restarts, "7d", "10.0.0.1", fmt.Sprintf("node-%d", i%max(nodes, 1)), "<none>", "<none>"},
		"object": map[string]any{"kind": "PartialObjectMetadata", "apiVersion": "meta.k8s.io/v1", "metadata": obj["metadata"]},
	}
}

// nodeObject is a node carrying nodeInfo and a typical status.images list
// (~5 KB JSON).
func nodeObject(i int, notReady bool) map[string]any {
	images := make([]any, 0, 40)
	for j := range 40 {
		images = append(images, map[string]any{"names": []any{fmt.Sprintf("registry.example.com/image-%d@sha256:%064d", j, j)}, "sizeBytes": 123456789})
	}
	ready := "True"
	if notReady {
		ready = "False"
	}
	return map[string]any{
		"kind": "Node", "apiVersion": "v1",
		"metadata": map[string]any{"name": fmt.Sprintf("node-%d", i), "creationTimestamp": "2026-11-01T00:00:00Z"},
		"status": map[string]any{
			"conditions": []any{map[string]any{"type": "Ready", "status": ready}},
			"nodeInfo": map[string]any{"kubeletVersion": "v1.30.4-eks-a737599", "osImage": "Bottlerocket OS 1.20.0 (aws-k8s-1.30)",
				"kernelVersion": "6.1.90", "containerRuntimeVersion": "containerd://1.7.13+bottlerocket", "architecture": "arm64"},
			"images": images,
		},
	}
}
