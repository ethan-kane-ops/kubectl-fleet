//go:build e2e

package e2e

import (
	"path/filepath"
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
)

// mixedKubeconfig merges the real e2e kubeconfig with one extra context
// pointing at an unreachable loopback port, so --strict can be proven against
// a realistic fleet: two real, healthy clusters plus one that's down. This
// exercises the actual process exit code an operator's script would see,
// which is the entire point of --strict.
func mixedKubeconfig(t *testing.T) string {
	t.Helper()
	cfg, err := clientcmd.LoadFromFile(kubeconfig)
	if err != nil {
		t.Fatalf("load base kubeconfig: %v", err)
	}
	cfg.Clusters["c-unreachable"] = &api.Cluster{Server: "https://127.0.0.1:1"}
	cfg.AuthInfos["unused"] = &api.AuthInfo{}
	cfg.Contexts["kind-fleet-e2e-unreachable"] = &api.Context{Cluster: "c-unreachable", AuthInfo: "unused"}

	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := clientcmd.WriteToFile(*cfg, path); err != nil {
		t.Fatalf("write mixed kubeconfig: %v", err)
	}
	return path
}

func TestE2E_StrictFailsWithUnreachableContext(t *testing.T) {
	mixed := mixedKubeconfig(t)

	_, _, code := run(t, mixed, "status", "--contexts", "^kind-fleet-e2e-", "--strict")
	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero: one of three contexts is unreachable")
	}

	_, _, code = run(t, mixed, "status", "--contexts", "^kind-fleet-e2e-")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0: per-cluster failures shouldn't fail the command without --strict", code)
	}
}
