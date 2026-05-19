package printers

import (
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestFor(t *testing.T) {
	cases := []struct {
		name   string
		alias  string
		wantOK bool
		header string
	}{
		{"pod plural", "pods", true, "RESTARTS"},
		{"pod singular", "pod", true, "RESTARTS"},
		{"pod short", "po", true, "RESTARTS"},
		{"deployment short", "deploy", true, "UP-TO-DATE"},
		{"service short", "svc", true, "PORT(S)"},
		{"node plural", "nodes", true, "VERSION"},
		{"upper", "PODS", true, "RESTARTS"},
		{"unknown", "configmap", false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, ok := For(c.alias)
			if ok != c.wantOK {
				t.Fatalf("For(%q) ok=%v want %v", c.alias, ok, c.wantOK)
			}
			if !ok {
				return
			}
			found := false
			for _, h := range s.Headers {
				if h == c.header {
					found = true
				}
			}
			if !found {
				t.Fatalf("Schema for %q missing header %q in %v", c.alias, c.header, s.Headers)
			}
		})
	}
}

func TestGeneric(t *testing.T) {
	s := Generic()
	if got := strings.Join(s.Headers, ","); got != "NAME,AGE" {
		t.Fatalf("generic headers = %q", got)
	}
	u := newUnstructured(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "ConfigMap",
		"metadata":   map[string]interface{}{"name": "cm-x"},
	})
	cols, wide := s.Row(u)
	if cols[0] != "cm-x" {
		t.Fatalf("name col = %q", cols[0])
	}
	if wide[0] != "ConfigMap" || wide[1] != "v1" {
		t.Fatalf("wide cols = %v", wide)
	}
}

func TestAge(t *testing.T) {
	now := time.Now()
	cases := []struct {
		offset time.Duration
		want   string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{3 * time.Hour, "3h"},
		{72 * time.Hour, "3d"},
	}
	for _, c := range cases {
		got := Age(metav1.NewTime(now.Add(-c.offset)))
		if got != c.want {
			t.Errorf("Age(-%v) = %q want %q", c.offset, got, c.want)
		}
	}
	if Age(metav1.Time{}) != "" {
		t.Errorf("zero time should render empty")
	}
}

func TestPodRow(t *testing.T) {
	u := newUnstructured(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]interface{}{"name": "app-1", "namespace": "default"},
		"spec": map[string]interface{}{
			"containers": []interface{}{
				map[string]interface{}{"name": "main"},
				map[string]interface{}{"name": "sidecar"},
			},
			"nodeName": "node-a",
		},
		"status": map[string]interface{}{
			"phase": "Running",
			"podIP": "10.0.0.5",
			"containerStatuses": []interface{}{
				map[string]interface{}{"name": "main", "ready": true, "restartCount": int64(2)},
				map[string]interface{}{"name": "sidecar", "ready": false, "restartCount": int64(0),
					"state": map[string]interface{}{
						"waiting": map[string]interface{}{"reason": "CrashLoopBackOff"},
					},
				},
			},
		},
	})
	s, ok := For("pods")
	if !ok {
		t.Fatal("pods schema not registered")
	}
	cols, wide := s.Row(u)
	got := map[string]string{
		"NAME":     cols[0],
		"READY":    cols[1],
		"STATUS":   cols[2],
		"RESTARTS": cols[3],
		"IP":       wide[0],
		"NODE":     wide[1],
	}
	wantPairs := map[string]string{
		"NAME":     "app-1",
		"READY":    "1/2",
		"STATUS":   "CrashLoopBackOff",
		"RESTARTS": "2",
		"IP":       "10.0.0.5",
		"NODE":     "node-a",
	}
	for k, v := range wantPairs {
		if got[k] != v {
			t.Errorf("col %s = %q want %q", k, got[k], v)
		}
	}
}

func TestPodRow_phaseFallback(t *testing.T) {
	u := newUnstructured(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]interface{}{"name": "ok-pod"},
		"spec":       map[string]interface{}{"containers": []interface{}{map[string]interface{}{"name": "c"}}},
		"status": map[string]interface{}{
			"phase": "Running",
			"containerStatuses": []interface{}{
				map[string]interface{}{"name": "c", "ready": true, "restartCount": int64(0)},
			},
		},
	})
	s, _ := For("pods")
	cols, _ := s.Row(u)
	if cols[2] != "Running" {
		t.Fatalf("STATUS = %q want Running", cols[2])
	}
}

func TestDeploymentRow(t *testing.T) {
	u := newUnstructured(map[string]interface{}{
		"apiVersion": "apps/v1",
		"kind":       "Deployment",
		"metadata":   map[string]interface{}{"name": "api"},
		"spec": map[string]interface{}{
			"replicas": int64(3),
			"selector": map[string]interface{}{"matchLabels": map[string]interface{}{"app": "api"}},
			"template": map[string]interface{}{
				"spec": map[string]interface{}{
					"containers": []interface{}{
						map[string]interface{}{"name": "api", "image": "ghcr.io/example/api:v1"},
					},
				},
			},
		},
		"status": map[string]interface{}{
			"readyReplicas":     int64(3),
			"updatedReplicas":   int64(3),
			"availableReplicas": int64(3),
		},
	})
	s, _ := For("deployments")
	cols, wide := s.Row(u)
	if cols[1] != "3/3" {
		t.Errorf("READY = %q", cols[1])
	}
	if cols[2] != "3" {
		t.Errorf("UP-TO-DATE = %q", cols[2])
	}
	if wide[0] != "api" {
		t.Errorf("CONTAINERS = %q", wide[0])
	}
	if wide[1] != "ghcr.io/example/api:v1" {
		t.Errorf("IMAGES = %q", wide[1])
	}
	if wide[2] != "app=api" {
		t.Errorf("SELECTOR = %q", wide[2])
	}
}

func TestServiceRow_loadBalancer(t *testing.T) {
	u := newUnstructured(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   map[string]interface{}{"name": "lb"},
		"spec": map[string]interface{}{
			"type":      "LoadBalancer",
			"clusterIP": "10.0.0.50",
			"ports": []interface{}{
				map[string]interface{}{"port": int64(80), "protocol": "TCP", "nodePort": int64(31234)},
			},
			"selector": map[string]interface{}{"app": "web"},
		},
		"status": map[string]interface{}{
			"loadBalancer": map[string]interface{}{
				"ingress": []interface{}{
					map[string]interface{}{"ip": "203.0.113.10"},
				},
			},
		},
	})
	s, _ := For("svc")
	cols, wide := s.Row(u)
	if cols[1] != "LoadBalancer" {
		t.Errorf("TYPE = %q", cols[1])
	}
	if cols[3] != "203.0.113.10" {
		t.Errorf("EXTERNAL-IP = %q", cols[3])
	}
	if cols[4] != "80:31234/TCP" {
		t.Errorf("PORT(S) = %q", cols[4])
	}
	if wide[0] != "app=web" {
		t.Errorf("SELECTOR = %q", wide[0])
	}
}

func TestServiceRow_clusterIPDefault(t *testing.T) {
	u := newUnstructured(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Service",
		"metadata":   map[string]interface{}{"name": "default-type"},
		"spec": map[string]interface{}{
			"clusterIP": "10.0.0.99",
			"ports":     []interface{}{map[string]interface{}{"port": int64(8080)}},
		},
	})
	s, _ := For("services")
	cols, _ := s.Row(u)
	if cols[1] != "ClusterIP" {
		t.Errorf("TYPE default = %q want ClusterIP", cols[1])
	}
	if cols[3] != "<none>" {
		t.Errorf("EXTERNAL-IP = %q want <none>", cols[3])
	}
	if cols[4] != "8080/TCP" {
		t.Errorf("PORT(S) = %q", cols[4])
	}
}

func TestNodeRow(t *testing.T) {
	u := newUnstructured(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata": map[string]interface{}{
			"name": "node-1",
			"labels": map[string]interface{}{
				"node-role.kubernetes.io/control-plane": "",
			},
		},
		"status": map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "Ready", "status": "True"},
				map[string]interface{}{"type": "MemoryPressure", "status": "False"},
			},
			"nodeInfo": map[string]interface{}{
				"kubeletVersion":          "v1.31.2",
				"osImage":                 "Ubuntu 24.04",
				"kernelVersion":           "6.8.0",
				"containerRuntimeVersion": "containerd://1.7.18",
			},
			"addresses": []interface{}{
				map[string]interface{}{"type": "InternalIP", "address": "192.168.1.10"},
			},
		},
	})
	s, _ := For("nodes")
	cols, wide := s.Row(u)
	if cols[1] != "Ready" {
		t.Errorf("STATUS = %q", cols[1])
	}
	if cols[2] != "control-plane" {
		t.Errorf("ROLES = %q", cols[2])
	}
	if cols[4] != "v1.31.2" {
		t.Errorf("VERSION = %q", cols[4])
	}
	if wide[0] != "192.168.1.10" {
		t.Errorf("INTERNAL-IP = %q", wide[0])
	}
	if wide[1] != "<none>" {
		t.Errorf("EXTERNAL-IP = %q", wide[1])
	}
}

func TestNodeRow_notReadyWithPressure(t *testing.T) {
	u := newUnstructured(map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata":   map[string]interface{}{"name": "node-2"},
		"status": map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{"type": "Ready", "status": "False"},
				map[string]interface{}{"type": "DiskPressure", "status": "True"},
			},
		},
	})
	s, _ := For("nodes")
	cols, _ := s.Row(u)
	if !strings.HasPrefix(cols[1], "NotReady") {
		t.Errorf("STATUS = %q want NotReady prefix", cols[1])
	}
	if !strings.Contains(cols[1], "DiskPressure") {
		t.Errorf("STATUS should include DiskPressure, got %q", cols[1])
	}
}

func newUnstructured(content map[string]interface{}) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: content}
}
