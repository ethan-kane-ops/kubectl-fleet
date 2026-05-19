package printers

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func init() {
	register(Schema{
		Headers:     []string{"NAME", "TYPE", "CLUSTER-IP", "EXTERNAL-IP", "PORT(S)", "AGE"},
		WideHeaders: []string{"SELECTOR"},
		Row:         serviceRow,
		Namespaced:  true,
	}, "services", "service", "svc")
}

func serviceRow(u *unstructured.Unstructured) ([]string, []string) {
	obj := u.UnstructuredContent()
	typ := stringFromObj(obj, "spec", "type")
	if typ == "" {
		typ = "ClusterIP"
	}
	cols := []string{
		u.GetName(),
		typ,
		clusterIP(obj),
		externalIP(obj, typ),
		servicePorts(obj),
		Age(u.GetCreationTimestamp()),
	}
	wide := []string{serviceSelector(obj)}
	return cols, wide
}

func clusterIP(obj map[string]interface{}) string {
	v := stringFromObj(obj, "spec", "clusterIP")
	if v == "" {
		return "<none>"
	}
	return v
}

func externalIP(obj map[string]interface{}, typ string) string {
	switch typ {
	case "ExternalName":
		if v := stringFromObj(obj, "spec", "externalName"); v != "" {
			return v
		}
	case "LoadBalancer":
		ingress := sliceFromObj(obj, "status", "loadBalancer", "ingress")
		var ips []string
		for _, i := range ingress {
			m := asMap(i)
			if ip, _ := m["ip"].(string); ip != "" {
				ips = append(ips, ip)
				continue
			}
			if h, _ := m["hostname"].(string); h != "" {
				ips = append(ips, h)
			}
		}
		if len(ips) > 0 {
			return strings.Join(ips, ",")
		}
		return "<pending>"
	}
	if ips := sliceFromObj(obj, "spec", "externalIPs"); len(ips) > 0 {
		parts := make([]string, 0, len(ips))
		for _, ip := range ips {
			if s, ok := ip.(string); ok {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ",")
	}
	return "<none>"
}

func servicePorts(obj map[string]interface{}) string {
	ports := sliceFromObj(obj, "spec", "ports")
	parts := make([]string, 0, len(ports))
	for _, p := range ports {
		m := asMap(p)
		port, _ := m["port"].(int64)
		if port == 0 {
			if f, ok := m["port"].(float64); ok {
				port = int64(f)
			}
		}
		proto, _ := m["protocol"].(string)
		if proto == "" {
			proto = "TCP"
		}
		seg := fmt.Sprintf("%d/%s", port, proto)
		if np, _ := m["nodePort"].(int64); np != 0 {
			seg = fmt.Sprintf("%d:%d/%s", port, np, proto)
		} else if f, ok := m["nodePort"].(float64); ok && f != 0 {
			seg = fmt.Sprintf("%d:%d/%s", port, int64(f), proto)
		}
		parts = append(parts, seg)
	}
	if len(parts) == 0 {
		return "<none>"
	}
	return strings.Join(parts, ",")
}

func serviceSelector(obj map[string]interface{}) string {
	m, found, err := unstructured.NestedMap(obj, "spec", "selector")
	if err != nil || !found {
		return "<none>"
	}
	if len(m) == 0 {
		return "<none>"
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(parts, ",")
}
