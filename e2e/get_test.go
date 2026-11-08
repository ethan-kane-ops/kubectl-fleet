//go:build e2e

package e2e

import "testing"

type objectList struct {
	Kind  string `json:"kind"`
	Items []struct {
		Metadata struct {
			Name        string            `json:"name"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Status struct {
			ReadyReplicas int `json:"readyReplicas"`
		} `json:"status"`
	} `json:"items"`
}

func TestE2E_Get(t *testing.T) {
	list, code := runJSON[objectList](t, "get", "deploy", "api", "-n", "payments", "--contexts", "^kind-fleet-e2e-")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if list.Kind != "List" {
		t.Errorf("kind = %q, want List", list.Kind)
	}

	// payments/api only exists on cluster A. Absent from B is not an error,
	// so B contributes no item at all.
	if len(list.Items) != 1 {
		t.Fatalf("got %d items, want 1 (from %s): %+v", len(list.Items), contextA, list.Items)
	}
	it := list.Items[0]
	if ctx := it.Metadata.Annotations["kubectl-fleet/context"]; ctx != contextA {
		t.Errorf("context annotation = %q, want %s", ctx, contextA)
	}
	if it.Metadata.Name != "api" {
		t.Errorf("name = %q, want api", it.Metadata.Name)
	}
	if it.Status.ReadyReplicas != 2 {
		t.Errorf("readyReplicas = %d, want 2", it.Status.ReadyReplicas)
	}
}
