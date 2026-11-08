package printers

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func init() {
	register(Schema{
		Headers:     []string{"NAME", "READY", "STATUS", "RESTARTS", "AGE"},
		WideHeaders: []string{"IP", "NODE", "NOMINATED NODE", "READINESS GATES"},
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

// podStatus follows the STATUS logic of `kubectl get pod` (printPod):
// status.reason, else phase, overridden by the first init container that has
// not finished, else by the main containers' waiting or terminated reasons,
// and finally by Terminating while a delete is pending.
func podStatus(obj map[string]interface{}) string {
	reason := stringFromObj(obj, "status", "reason")
	if reason == "" {
		reason = stringFromObj(obj, "status", "phase")
	}

	initSpecs := sliceFromObj(obj, "spec", "initContainers")
	initializing := false
	for i, cs := range sliceFromObj(obj, "status", "initContainerStatuses") {
		m := asMap(cs)
		state := asMap(m["state"])
		term := asMap(state["terminated"])
		waitReason, _ := asMap(state["waiting"])["reason"].(string)
		switch {
		case term != nil && numFrom(term, "exitCode") == 0:
			continue
		case term != nil:
			reason = "Init:" + terminatedReason(term)
		case waitReason != "" && waitReason != "PodInitializing":
			reason = "Init:" + waitReason
		default:
			// A native sidecar (restartPolicy Always) that has started keeps
			// running for the pod's life; it does not hold up initialisation.
			if i < len(initSpecs) && asMap(initSpecs[i])["restartPolicy"] == "Always" && m["started"] == true {
				continue
			}
			reason = fmt.Sprintf("Init:%d/%d", i, len(initSpecs))
		}
		initializing = true
		break
	}

	if !initializing {
		hasRunning := false
		statuses := sliceFromObj(obj, "status", "containerStatuses")
		for i := len(statuses) - 1; i >= 0; i-- {
			m := asMap(statuses[i])
			state := asMap(m["state"])
			term := asMap(state["terminated"])
			waitReason, _ := asMap(state["waiting"])["reason"].(string)
			switch {
			case waitReason != "":
				reason = waitReason
			case term != nil:
				reason = terminatedReason(term)
			case state["running"] != nil && m["ready"] == true:
				hasRunning = true
			}
		}
		if reason == "Completed" && hasRunning {
			reason = "Running"
		}
	}

	if ts, _, _ := unstructured.NestedString(obj, "metadata", "deletionTimestamp"); ts != "" {
		if stringFromObj(obj, "status", "reason") == "NodeLost" {
			return "Unknown"
		}
		return "Terminating"
	}
	return reason
}

// terminatedReason renders a terminated container state as kubectl does:
// its reason, else Signal:N, else ExitCode:N.
func terminatedReason(term map[string]interface{}) string {
	if r, _ := term["reason"].(string); r != "" {
		return r
	}
	if sig := numFrom(term, "signal"); sig != 0 {
		return fmt.Sprintf("Signal:%d", sig)
	}
	return fmt.Sprintf("ExitCode:%d", numFrom(term, "exitCode"))
}

// numFrom reads an integer field that may decode as int64 or float64.
func numFrom(m map[string]interface{}, key string) int64 {
	switch v := m[key].(type) {
	case int64:
		return v
	case float64:
		return int64(v)
	}
	return 0
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
