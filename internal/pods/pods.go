// Package pods counts a cluster's pods for `status` and `report` from four
// phase-filtered lists, so the bulk of a cluster (its Running pods) costs a
// cells-only table row each, tens of bytes on the wire, not a full object.
//
// Accepted gaps, fine for triage: the four lists are read at slightly
// different moments, so a pod changing phase between them can be counted
// twice or missed; and a Running pod's crashloop is judged from its table
// row, which shows one container's state and the pod's total restarts.
package pods

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
)

// Field selectors for the four lists. Together they cover every pod once.
const (
	SelectRunning = "status.phase=Running"
	SelectPending = "status.phase=Pending"
	SelectFailed  = "status.phase=Failed"
	SelectOther   = "status.phase!=Running,status.phase!=Pending,status.phase!=Failed"
)

// Source lists the pods matching a field selector, as table cells or as
// full objects.
type Source interface {
	// Rows calls fn with each pod's STATUS and RESTARTS cells. It returns
	// k8s.ErrNoTable when the server cannot render tables.
	Rows(ctx context.Context, fieldSelector string, fn func(status, restarts string) error) error
	// Pods calls fn with each full pod.
	Pods(ctx context.Context, fieldSelector string, fn func(*corev1.Pod) error) error
}

// Options tunes Count.
type Options struct {
	// PendingFor is how long a pod must be Pending to count as stuck; zero
	// disables stuck counting.
	PendingFor time.Duration
	// Now is the time pod ages are measured against.
	Now time.Time
	// Since, when set, reads Running pods in full and counts containers
	// whose last termination finished within the window.
	Since time.Duration
}

// Counts is what a cluster's pod lists add up to.
type Counts struct {
	Running, Pending, StuckPending, CrashLoop, Failed, Total int
	RestartedInWindow                                        int
	// StuckPendingNS counts stuck Pending pods per namespace.
	StuckPendingNS map[string]int
	// NoisyNS counts Pending and Failed pods per namespace. Crashlooping
	// Running pods are not in it: their table rows carry no namespace.
	NoisyNS map[string]int
}

// Count reads the four lists concurrently and adds them up. A failed list
// fails the count; the others are cancelled.
func Count(ctx context.Context, src Source, opts Options) (Counts, error) {
	var running, pending, failed, other Counts
	g, ctx := errgroup.WithContext(ctx)
	g.Go(func() error { return wrap("running", countRunning(ctx, src, opts, &running)) })
	g.Go(func() error { return wrap("pending", countPending(ctx, src, opts, &pending)) })
	g.Go(func() error { return wrap("failed", countFailed(ctx, src, opts, &failed)) })
	g.Go(func() error { return wrap("other", countOther(ctx, src, &other)) })
	if err := g.Wait(); err != nil {
		return Counts{}, err
	}
	c := Counts{
		Running:           running.Running,
		Pending:           pending.Pending,
		StuckPending:      pending.StuckPending,
		Failed:            failed.Failed,
		CrashLoop:         running.CrashLoop + pending.CrashLoop + failed.CrashLoop,
		RestartedInWindow: running.RestartedInWindow + pending.RestartedInWindow + failed.RestartedInWindow,
		StuckPendingNS:    pending.StuckPendingNS,
		NoisyNS:           pending.NoisyNS,
	}
	if c.StuckPendingNS == nil {
		c.StuckPendingNS = map[string]int{}
	}
	if c.NoisyNS == nil {
		c.NoisyNS = map[string]int{}
	}
	for ns, n := range failed.NoisyNS {
		c.NoisyNS[ns] += n
	}
	c.Total = c.Running + c.Pending + c.Failed + other.Total
	return c, nil
}

func wrap(list string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("list %s pods: %w", list, err)
}

// addRestarts counts p's containers restarted within opts.Since, when set.
func addRestarts(c *Counts, p *corev1.Pod, opts Options) {
	if opts.Since > 0 {
		c.RestartedInWindow += RestartsInWindow(p, opts.Now.Add(-opts.Since))
	}
}

func countRunning(ctx context.Context, src Source, opts Options, c *Counts) error {
	full := func() error {
		return src.Pods(ctx, SelectRunning, func(p *corev1.Pod) error {
			c.Running++
			if IsCrashLoop(p) {
				c.CrashLoop++
			}
			addRestarts(c, p, opts)
			return nil
		})
	}
	if opts.Since > 0 {
		return full()
	}
	err := src.Rows(ctx, SelectRunning, func(status, restarts string) error {
		c.Running++
		if CrashLoopRow(status, ParseRestarts(restarts)) {
			c.CrashLoop++
		}
		return nil
	})
	if errors.Is(err, k8s.ErrNoTable) {
		*c = Counts{}
		return full()
	}
	return err
}

func countPending(ctx context.Context, src Source, opts Options, c *Counts) error {
	c.StuckPendingNS, c.NoisyNS = map[string]int{}, map[string]int{}
	return src.Pods(ctx, SelectPending, func(p *corev1.Pod) error {
		c.Pending++
		c.NoisyNS[p.Namespace]++
		// A creation time in the future (clock skew) gives a negative age,
		// which is never stuck.
		if opts.PendingFor > 0 && opts.Now.Sub(p.CreationTimestamp.Time) >= opts.PendingFor {
			c.StuckPending++
			c.StuckPendingNS[p.Namespace]++
		}
		if IsCrashLoop(p) {
			c.CrashLoop++
		}
		addRestarts(c, p, opts)
		return nil
	})
}

func countFailed(ctx context.Context, src Source, opts Options, c *Counts) error {
	c.NoisyNS = map[string]int{}
	return src.Pods(ctx, SelectFailed, func(p *corev1.Pod) error {
		c.Failed++
		c.NoisyNS[p.Namespace]++
		if IsCrashLoop(p) {
			c.CrashLoop++
		}
		addRestarts(c, p, opts)
		return nil
	})
}

func countOther(ctx context.Context, src Source, c *Counts) error {
	err := src.Rows(ctx, SelectOther, func(string, string) error {
		c.Total++
		return nil
	})
	if errors.Is(err, k8s.ErrNoTable) {
		c.Total = 0
		return src.Pods(ctx, SelectOther, func(*corev1.Pod) error {
			c.Total++
			return nil
		})
	}
	return err
}

// terminatedReasons are the STATUS values the pod table shows for a
// container that exited non-zero.
var terminatedReasons = map[string]bool{"Error": true, "OOMKilled": true, "ContainerCannotRun": true, "StartError": true}

// CrashLoopRow applies IsCrashLoop's rule to a pod table row: a container in
// back-off, or one that exited non-zero after restarting. The table shows
// one container's state for the whole pod, so a second looping container
// behind another reason is missed.
func CrashLoopRow(status string, restarts int) bool {
	if strings.HasSuffix(status, "CrashLoopBackOff") {
		return true
	}
	if restarts == 0 {
		return false
	}
	s := strings.TrimPrefix(status, "Init:")
	return terminatedReasons[s] || strings.HasPrefix(s, "ExitCode:") || strings.HasPrefix(s, "Signal:")
}

// ParseRestarts reads a RESTARTS cell such as "5" or "5 (3m ago)"; anything
// else is 0.
func ParseRestarts(cell string) int {
	n, _ := strconv.Atoi(strings.Fields(cell + " x")[0])
	return n
}

// IsCrashLoop reports a pod with a container, init containers included, that
// keeps exiting. The API shows such a container two ways depending on when it
// is read: Waiting/CrashLoopBackOff during the kubelet's back-off, and
// Terminated with a non-zero exit in the gap between an exit and the back-off
// starting. A fast-failing container spends most of its first minutes in the
// second, so checking only the first reports a looping pod as healthy.
// RestartCount > 0 keeps out a container that failed once and is not being
// restarted, such as a Job's pod under restartPolicy Never.
func IsCrashLoop(p *corev1.Pod) bool {
	for _, list := range [][]corev1.ContainerStatus{p.Status.InitContainerStatuses, p.Status.ContainerStatuses} {
		for _, cs := range list {
			if cs.State.Waiting != nil && cs.State.Waiting.Reason == "CrashLoopBackOff" {
				return true
			}
			if t := cs.State.Terminated; t != nil && t.ExitCode != 0 && cs.RestartCount > 0 {
				return true
			}
		}
	}
	return false
}

// RestartsInWindow returns the number of containers in p whose last
// termination happened after cutoff. A container restarting many times in the
// window still counts once because only the most recent termination carries
// a timestamp: accept the floor, the metric is a triage signal, not a SLA.
func RestartsInWindow(p *corev1.Pod, cutoff time.Time) int {
	n := 0
	for _, list := range [][]corev1.ContainerStatus{p.Status.ContainerStatuses, p.Status.InitContainerStatuses} {
		for _, cs := range list {
			if t := cs.LastTerminationState.Terminated; t != nil && t.FinishedAt.After(cutoff) {
				n++
			}
		}
	}
	return n
}
