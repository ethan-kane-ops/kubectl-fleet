package printers

import (
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func init() {
	register(Schema{
		Headers:     []string{"NAME", "STATUS", "ROLES", "AGE", "VERSION"},
		WideHeaders: []string{"INTERNAL-IP", "EXTERNAL-IP", "OS-IMAGE", "KERNEL-VERSION", "CONTAINER-RUNTIME"},
		Row:         nodeRow,
	}, "nodes", "node", "no")
}

func nodeRow(u *unstructured.Unstructured) ([]string, []string) {
	obj := u.UnstructuredContent()
	cols := []string{
		u.GetName(),
		nodeStatus(obj),
		nodeRoles(u.GetLabels()),
		Age(u.GetCreationTimestamp()),
		stringFromObj(obj, "status", "nodeInfo", "kubeletVersion"),
	}
	internal, external := nodeAddresses(obj)
	wide := []string{
		internal,
		external,
		stringFromObj(obj, "status", "nodeInfo", "osImage"),
		stringFromObj(obj, "status", "nodeInfo", "kernelVersion"),
		stringFromObj(obj, "status", "nodeInfo", "containerRuntimeVersion"),
	}
	return cols, wide
}

func nodeStatus(obj map[string]interface{}) string {
	// Combine condition states. NotReady wins; Ready maps to "Ready"; other
	// problematic conditions (MemoryPressure, DiskPressure, etc.) append.
	ready := false
	var extras []string
	for _, c := range sliceFromObj(obj, "status", "conditions") {
		m := asMap(c)
		t, _ := m["type"].(string)
		s, _ := m["status"].(string)
		if t == "Ready" {
			ready = s == "True"
		} else if s == "True" {
			extras = append(extras, t)
		}
	}
	state := "Ready"
	if !ready {
		state = "NotReady"
	}
	if len(extras) > 0 {
		sort.Strings(extras)
		state += "," + strings.Join(extras, ",")
	}
	return state
}

func nodeRoles(labels map[string]string) string {
	const prefix = "node-role.kubernetes.io/"
	var roles []string
	for k := range labels {
		if strings.HasPrefix(k, prefix) {
			roles = append(roles, strings.TrimPrefix(k, prefix))
		}
	}
	if len(roles) == 0 {
		return "<none>"
	}
	sort.Strings(roles)
	return strings.Join(roles, ",")
}

func nodeAddresses(obj map[string]interface{}) (internal, external string) {
	for _, a := range sliceFromObj(obj, "status", "addresses") {
		m := asMap(a)
		t, _ := m["type"].(string)
		addr, _ := m["address"].(string)
		switch t {
		case "InternalIP":
			internal = addr
		case "ExternalIP":
			external = addr
		}
	}
	if internal == "" {
		internal = "<none>"
	}
	if external == "" {
		external = "<none>"
	}
	return internal, external
}
