//go:build e2e

// Package e2e runs the compiled kubectl-fleet binary against two real kind
// clusters. It is excluded from `go test ./...` and `go vet ./...` (no
// build tag => the package has no files to compile) and only runs via
// `go test -tags e2e`, wired up by the justfile's e2e-test/e2e recipes.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	contextA = "kind-fleet-e2e-a"
	contextB = "kind-fleet-e2e-b"
)

var (
	binary     string
	kubeconfig string
)

func TestMain(m *testing.M) {
	binary = os.Getenv("FLEET_BINARY")
	kubeconfig = os.Getenv("FLEET_E2E_KUBECONFIG")
	if binary == "" || kubeconfig == "" {
		fmt.Fprintln(os.Stderr, "e2e: FLEET_BINARY and FLEET_E2E_KUBECONFIG must be set; run via `just e2e` or `just e2e-test`")
		os.Exit(1)
	}
	if err := waitForFixtures(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e: fixtures not ready:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

const fixtureWaitTimeout = 2 * time.Minute

// waitForFixtures blocks until cluster A's healthy Deployment is fully
// Available and cluster B's crashing Deployment has produced at least one
// container restart, so tests don't race the scheduler/kubelet on a
// freshly created cluster. Each cluster gets its own independent timeout:
// a slow CI runner making cluster A's wait take most of a shared budget
// must not starve cluster B's wait of time it never got to use.
func waitForFixtures() error {
	csA, err := clientFor(contextA)
	if err != nil {
		return fmt.Errorf("client for %s: %w", contextA, err)
	}
	csB, err := clientFor(contextB)
	if err != nil {
		return fmt.Errorf("client for %s: %w", contextB, err)
	}

	ctxA, cancelA := context.WithTimeout(context.Background(), fixtureWaitTimeout)
	defer cancelA()
	err = wait.PollUntilContextTimeout(ctxA, 2*time.Second, fixtureWaitTimeout, true, func(ctx context.Context) (bool, error) {
		d, err := csA.AppsV1().Deployments("payments").Get(ctx, "api", metav1.GetOptions{})
		if err != nil {
			return false, nil //nolint:nilerr // transient: keep polling until timeout
		}
		return d.Spec.Replicas != nil && d.Status.AvailableReplicas == *d.Spec.Replicas, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for %s payments/api to become available: %w", contextA, err)
	}

	ctxB, cancelB := context.WithTimeout(context.Background(), fixtureWaitTimeout)
	defer cancelB()
	err = wait.PollUntilContextTimeout(ctxB, 2*time.Second, fixtureWaitTimeout, true, func(ctx context.Context) (bool, error) {
		pods, err := csB.CoreV1().Pods("payments").List(ctx, metav1.ListOptions{LabelSelector: "app=crashy"})
		if err != nil || len(pods.Items) == 0 {
			return false, nil //nolint:nilerr // transient: keep polling until timeout
		}
		for _, cs := range pods.Items[0].Status.ContainerStatuses {
			// health.isCrashLoop keys off this exact waiting reason, not just
			// RestartCount>0: a pod can be transiently Running again between
			// restarts, so wait for the actual backoff state `status` checks.
			if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for %s payments/crashy to enter CrashLoopBackOff: %w", contextB, err)
	}
	return nil
}

func clientFor(ctxName string) (*kubernetes.Clientset, error) {
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig},
		&clientcmd.ConfigOverrides{CurrentContext: ctxName},
	).ClientConfig()
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(cfg)
}

// run executes the built binary against the e2e kubeconfig and returns
// stdout, stderr, and the process exit code. It does not treat a non-zero
// exit as a test failure: --strict's whole contract is a process exit code,
// so tests assert on code directly rather than on an in-process error value.
func run(t *testing.T, kubeconfigPath string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(binary, append([]string{"--kubeconfig", kubeconfigPath}, args...)...)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	return out.String(), errOut.String(), code
}

func runJSON(t *testing.T, args ...string) ([]map[string]string, int) {
	t.Helper()
	out, stderr, code := run(t, kubeconfig, append(args, "-o", "json")...)
	var rows []map[string]string
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("invalid json (stderr=%s): %v\n%s", stderr, err, out)
	}
	return rows, code
}
