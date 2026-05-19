package printers

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func init() {
	register(Schema{
		Headers:     []string{"NAME", "READY", "STATUS", "RESTARTS", "AGE"},
		WideHeaders: []string{"IP", "NODE", "NOMINATED_NODE", "READINESS_GATES"},
		Row:         podRow,
		Namespaced:  true,
	}, "pods", "pod", "po")
}

func podRow(u *unstructured.Unstructured) ([]string, []string) {
	obj := u.UnstructuredContent()
	ready, total, restarts := podCounts(obj)
	status := podStatus(obj)
	cols := []string{
		u.GetName(),
		fmt.Sprintf("%d/%d", ready, total),
		status,
		fmt.Sprintf("%d", restarts),
		Age(u.GetCreationTimestamp()),
	}
	wide := []string{
		stringFromObj(obj, "status", "podIP"),
		stringFromObj(obj, "spec", "nodeName"),
		stringFromObj(obj, "status", "nominatedNodeName"),
		readinessGates(obj),
	}
	return cols, wide
}

func podCounts(obj map[string]interface{}) (ready, total, restarts int) {
	spec := sliceFromObj(obj, "spec", "containers")
	total = len(spec)
	for _, cs := range sliceFromObj(obj, "status", "containerStatuses") {
		m := asMap(cs)
		if m == nil {
			continue
		}
		if r, _ := m["ready"].(bool); r {
			ready++
		}
		if rc, ok := m["restartCount"].(int64); ok {
			restarts += int(rc)
		} else if rc, ok := m["restartCount"].(float64); ok {
			restarts += int(rc)
		}
	}
	return ready, total, restarts
}

// podStatus mirrors a simplified subset of `kubectl get pod` STATUS logic.
// Order: any waiting reason on a container > any terminated non-zero reason >
// pod phase. Init container failures get an "Init:" prefix.
func podStatus(obj map[string]interface{}) string {
	if r := containerReason(sliceFromObj(obj, "status", "initContainerStatuses")); r != "" {
		return "Init:" + r
	}
	if r := containerReason(sliceFromObj(obj, "status", "containerStatuses")); r != "" {
		return r
	}
	if phase := stringFromObj(obj, "status", "phase"); phase != "" {
		return phase
	}
	return ""
}

func containerReason(statuses []interface{}) string {
	for _, cs := range statuses {
		m := asMap(cs)
		if m == nil {
			continue
		}
		state, _ := m["state"].(map[string]interface{})
		if w, ok := state["waiting"].(map[string]interface{}); ok {
			if r, _ := w["reason"].(string); r != "" {
				return r
			}
		}
		if t, ok := state["terminated"].(map[string]interface{}); ok {
			if r, _ := t["reason"].(string); r != "" {
				return r
			}
		}
	}
	return ""
}

func readinessGates(obj map[string]interface{}) string {
	gates := sliceFromObj(obj, "spec", "readinessGates")
	if len(gates) == 0 {
		return ""
	}
	conds := sliceFromObj(obj, "status", "conditions")
	condByType := map[string]string{}
	for _, c := range conds {
		m := asMap(c)
		t, _ := m["type"].(string)
		s, _ := m["status"].(string)
		condByType[t] = s
	}
	parts := make([]string, 0, len(gates))
	for _, g := range gates {
		m := asMap(g)
		t, _ := m["conditionType"].(string)
		s := condByType[t]
		if s == "" {
			s = "?"
		}
		parts = append(parts, fmt.Sprintf("%s=%s", t, s))
	}
	return strings.Join(parts, ",")
}
