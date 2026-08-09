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
	"strings"
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

// crashloopWaitTimeout is more generous than fixtureWaitTimeout. Diagnostics
// from a real CI failure showed the image pull itself is fast (~1s); the
// slow part is kubelet actually getting around to creating the container at
// all, which took ~2m48s on a GH Actions runner running two full kind
// control planes at once (CPU contention on the standard 2-vCPU runner).
// Once the container does start, it enters CrashLoopBackOff within a second,
// so this budget mostly needs to cover kubelet catching up, not the crash
// loop itself.
const crashloopWaitTimeout = 5 * time.Minute

// waitForFixtures blocks until cluster A's healthy Deployment is fully
// Available, so get/status tests don't race the scheduler on a freshly
// created cluster. Cluster B's crash-looping Deployment is deliberately
// NOT waited on here: CrashLoopBackOff is a state that continuously cycles
// (kubelet's backoff window shrinks back to near-zero on every restart
// attempt), so a one-time precondition check here could easily observe it
// true and then have it flip false again by the time a later test actually
// asserts on it. TestE2E_Status polls the real CLI output directly instead
// of trusting a point-in-time snapshot taken earlier.
func waitForFixtures() error {
	csA, err := clientFor(contextA)
	if err != nil {
		return fmt.Errorf("client for %s: %w", contextA, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), fixtureWaitTimeout)
	defer cancel()
	err = wait.PollUntilContextTimeout(ctx, 2*time.Second, fixtureWaitTimeout, true, func(ctx context.Context) (bool, error) {
		d, err := csA.AppsV1().Deployments("payments").Get(ctx, "api", metav1.GetOptions{})
		if err != nil {
			return false, nil //nolint:nilerr // transient: keep polling until timeout
		}
		return d.Spec.Replicas != nil && d.Status.AvailableReplicas == *d.Spec.Replicas, nil
	})
	if err != nil {
		return fmt.Errorf("waiting for %s payments/api to become available: %w", contextA, err)
	}
	return nil
}

// eventually retries fn until it returns true or timeout elapses, sleeping
// interval between attempts. Used for asserting on state that continuously
// cycles (like CrashLoopBackOff) rather than settling once. Reports success
// rather than failing directly, so callers can attach diagnostics (pod
// status/events) to the failure before calling t.Fatalf themselves.
func eventually(timeout, interval time.Duration, fn func() bool) bool {
	deadline := time.Now().Add(timeout)
	for {
		if fn() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(interval)
	}
}

// describePods returns a human-readable dump of pod status and recent
// namespace events for the given context/namespace/selector, for attaching
// to a test failure so a CI-only flake doesn't have to be diagnosed blind a
// second time.
func describePods(ctxName, namespace, selector string) string {
	cs, err := clientFor(ctxName)
	if err != nil {
		return fmt.Sprintf("describePods: client for %s: %v", ctxName, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var sb strings.Builder
	pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
	if err != nil {
		fmt.Fprintf(&sb, "list pods: %v\n", err)
	}
	for _, p := range pods.Items {
		fmt.Fprintf(&sb, "pod %s: phase=%s\n", p.Name, p.Status.Phase)
		for _, cs := range p.Status.ContainerStatuses {
			fmt.Fprintf(&sb, "  container %s: ready=%v restarts=%d", cs.Name, cs.Ready, cs.RestartCount)
			switch {
			case cs.State.Waiting != nil:
				fmt.Fprintf(&sb, " waiting=%s (%s)", cs.State.Waiting.Reason, cs.State.Waiting.Message)
			case cs.State.Running != nil:
				fmt.Fprintf(&sb, " running since=%s", cs.State.Running.StartedAt)
			case cs.State.Terminated != nil:
				fmt.Fprintf(&sb, " terminated=%s (exit %d)", cs.State.Terminated.Reason, cs.State.Terminated.ExitCode)
			}
			sb.WriteString("\n")
		}
	}

	events, err := cs.CoreV1().Events(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(&sb, "list events: %v\n", err)
	}
	for _, e := range events.Items {
		fmt.Fprintf(&sb, "event: %s %s/%s: %s (%s)\n", e.LastTimestamp, e.InvolvedObject.Kind, e.InvolvedObject.Name, e.Message, e.Reason)
	}
	return sb.String()
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
