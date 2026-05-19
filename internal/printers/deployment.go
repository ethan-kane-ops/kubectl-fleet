package printers

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func init() {
	register(Schema{
		Headers:     []string{"NAME", "READY", "UP-TO-DATE", "AVAILABLE", "AGE"},
		WideHeaders: []string{"CONTAINERS", "IMAGES", "SELECTOR"},
		Row:         deploymentRow,
		Namespaced:  true,
	}, "deployments", "deployment", "deploy")
}

func deploymentRow(u *unstructured.Unstructured) ([]string, []string) {
	obj := u.UnstructuredContent()
	desired := intFromObj(obj, "spec", "replicas")
	ready := intFromObj(obj, "status", "readyReplicas")
	updated := intFromObj(obj, "status", "updatedReplicas")
	available := intFromObj(obj, "status", "availableReplicas")

	cols := []string{
		u.GetName(),
		fmt.Sprintf("%d/%d", ready, desired),
		fmt.Sprintf("%d", updated),
		fmt.Sprintf("%d", available),
		Age(u.GetCreationTimestamp()),
	}
	names, images := podTemplateContainers(obj)
	wide := []string{names, images, deploymentSelector(obj)}
	return cols, wide
}

// podTemplateContainers reads .spec.template.spec.containers[].(name|image)
// and returns comma-joined name and image lists. Used by Deployment and other
// workload kinds that embed a PodTemplateSpec.
func podTemplateContainers(obj map[string]interface{}) (names, images string) {
	containers := sliceFromObj(obj, "spec", "template", "spec", "containers")
	if len(containers) == 0 {
		return "", ""
	}
	var ns, is []string
	for _, c := range containers {
		m := asMap(c)
		if n, _ := m["name"].(string); n != "" {
			ns = append(ns, n)
		}
		if i, _ := m["image"].(string); i != "" {
			is = append(is, i)
		}
	}
	return strings.Join(ns, ","), strings.Join(is, ",")
}

func deploymentSelector(obj map[string]interface{}) string {
	m, found, err := unstructured.NestedMap(obj, "spec", "selector", "matchLabels")
	if err != nil || !found {
		return ""
	}
	parts := make([]string, 0, len(m))
	for k, v := range m {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	return strings.Join(parts, ",")
}
