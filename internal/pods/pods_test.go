package pods

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
)

var t0 = time.Date(2026, 11, 14, 10, 30, 0, 0, time.UTC)

type row struct{ status, restarts string }

// fakeSource answers each field selector from fixed rows and pods.
type fakeSource struct {
	rows    map[string][]row
	pods    map[string][]*corev1.Pod
	noTable bool
	fail    map[string]error
	mu      sync.Mutex
	rowsAsk []string
}

func (f *fakeSource) Rows(_ context.Context, sel string, fn func(status, restarts string) error) error {
	f.mu.Lock()
	f.rowsAsk = append(f.rowsAsk, sel)
	f.mu.Unlock()
	if err := f.fail[sel]; err != nil {
		return err
	}
	if f.noTable {
		return k8s.ErrNoTable
	}
	for _, r := range f.rows[sel] {
		if err := fn(r.status, r.restarts); err != nil {
			return err
		}
	}
	return nil
}

func (f *fakeSource) Pods(ctx context.Context, sel string, fn func(*corev1.Pod) error) error {
	if err := f.fail[sel]; err != nil {
		return err
	}
	for _, p := range f.pods[sel] {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(p); err != nil {
			return err
		}
	}
	return nil
}

func pod(ns string, phase corev1.PodPhase, created time.Time, statuses ...corev1.ContainerStatus) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: ns, CreationTimestamp: metav1.NewTime(created)},
		Status:     corev1.PodStatus{Phase: phase, ContainerStatuses: statuses},
	}
}

var looping = corev1.ContainerStatus{RestartCount: 3, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}

func TestCrashLoopRow(t *testing.T) {
	cases := []struct {
		status   string
		restarts int
		want     bool
	}{
		{"CrashLoopBackOff", 0, true},
		{"Init:CrashLoopBackOff", 0, true},
		{"Error", 0, false},
		{"Error", 2, true},
		{"Init:Error", 1, true},
		{"OOMKilled", 3, true},
		{"ContainerCannotRun", 1, true},
		{"StartError", 1, true},
		{"ExitCode:137", 1, true},
		{"Signal:9", 1, true},
		{"ExitCode:137", 0, false},
		{"Running", 5, false},
		{"Completed", 1, false},
	}
	for _, c := range cases {
		if got := CrashLoopRow(c.status, c.restarts); got != c.want {
			t.Errorf("CrashLoopRow(%q, %d) = %v, want %v", c.status, c.restarts, got, c.want)
		}
	}
}

func TestParseRestarts(t *testing.T) {
	for in, want := range map[string]int{"0": 0, "5": 5, "5 (3m ago)": 5, "<none>": 0, "": 0} {
		if got := ParseRestarts(in); got != want {
			t.Errorf("ParseRestarts(%q) = %d, want %d", in, got, want)
		}
	}
}

func mixedSource() *fakeSource {
	return &fakeSource{
		rows: map[string][]row{
			SelectRunning: {{"Running", "0"}, {"Running", "0"}, {"Running", "1 (2d ago)"}, {"CrashLoopBackOff", "4 (1m ago)"}},
			SelectOther:   {{"Completed", "0"}, {"Completed", "0"}},
		},
		pods: map[string][]*corev1.Pod{
			SelectPending: {
				pod("a", corev1.PodPending, t0.Add(-10*time.Minute)),
				pod("a", corev1.PodPending, t0.Add(-time.Minute)),
				{
					ObjectMeta: metav1.ObjectMeta{Namespace: "c", CreationTimestamp: metav1.NewTime(t0.Add(-time.Minute))},
					Status:     corev1.PodStatus{Phase: corev1.PodPending, InitContainerStatuses: []corev1.ContainerStatus{looping}},
				},
			},
			SelectFailed: {pod("b", corev1.PodFailed, t0.Add(-time.Hour), looping)},
		},
	}
}

func TestCountMixes(t *testing.T) {
	got, err := Count(context.Background(), mixedSource(), Options{PendingFor: 5 * time.Minute, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	want := Counts{
		Running: 4, Pending: 3, StuckPending: 1, CrashLoop: 3, Failed: 1, Total: 10,
		StuckPendingNS: map[string]int{"a": 1},
		NoisyNS:        map[string]int{"a": 2, "b": 1, "c": 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Count =\n %+v\nwant\n %+v", got, want)
	}
}

func TestCountFallsBackWithoutTables(t *testing.T) {
	src := mixedSource()
	src.noTable = true
	src.pods[SelectRunning] = []*corev1.Pod{pod("x", corev1.PodRunning, t0), pod("x", corev1.PodRunning, t0, looping)}
	src.pods[SelectOther] = []*corev1.Pod{pod("x", corev1.PodSucceeded, t0)}
	got, err := Count(context.Background(), src, Options{PendingFor: 5 * time.Minute, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	if got.Running != 2 || got.CrashLoop != 3 || got.Total != 7 {
		t.Errorf("fallback counts %+v", got)
	}
}

func TestCountSinceReadsFullRunning(t *testing.T) {
	src := mixedSource()
	restarted := pod("x", corev1.PodRunning, t0, corev1.ContainerStatus{
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{FinishedAt: metav1.NewTime(t0.Add(-10 * time.Minute))}},
	})
	src.pods[SelectRunning] = []*corev1.Pod{restarted}
	got, err := Count(context.Background(), src, Options{PendingFor: 5 * time.Minute, Now: t0, Since: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	for _, sel := range src.rowsAsk {
		if sel == SelectRunning {
			t.Error("with Since, running pods must be read in full, not as a table")
		}
	}
	if got.Running != 1 || got.RestartedInWindow != 1 {
		t.Errorf("since counts %+v", got)
	}
}

func TestCountEmpty(t *testing.T) {
	got, err := Count(context.Background(), &fakeSource{}, Options{PendingFor: time.Minute, Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, Counts{StuckPendingNS: map[string]int{}, NoisyNS: map[string]int{}}) {
		t.Errorf("empty cluster counts %+v", got)
	}
}

func TestCountErrorWraps(t *testing.T) {
	src := mixedSource()
	boom := errors.New("boom")
	src.fail = map[string]error{SelectPending: boom}
	_, err := Count(context.Background(), src, Options{PendingFor: time.Minute, Now: t0})
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "list pending pods") {
		t.Errorf("want wrapped pending error, got %v", err)
	}
}

func TestIsCrashLoop(t *testing.T) {
	terminated := func(exit int32, restarts int32) corev1.ContainerStatus {
		return corev1.ContainerStatus{
			RestartCount: restarts,
			State:        corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: exit, Reason: "Error"}},
		}
	}
	backoff := corev1.ContainerStatus{
		RestartCount: 3,
		State:        corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
	}
	cases := []struct {
		name string
		init []corev1.ContainerStatus
		main []corev1.ContainerStatus
		want bool
	}{
		{"back-off", nil, []corev1.ContainerStatus{backoff}, true},
		{"between restarts", nil, []corev1.ContainerStatus{terminated(1, 3)}, true},
		{"init container looping", []corev1.ContainerStatus{terminated(1, 2)}, nil, true},
		{"failed once, not restarted", nil, []corev1.ContainerStatus{terminated(1, 0)}, false},
		{"completed after restarts", nil, []corev1.ContainerStatus{terminated(0, 2)}, false},
		{"running", nil, []corev1.ContainerStatus{{State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}}, false},
	}
	for _, c := range cases {
		p := &corev1.Pod{Status: corev1.PodStatus{InitContainerStatuses: c.init, ContainerStatuses: c.main}}
		if got := IsCrashLoop(p); got != c.want {
			t.Errorf("%s: isCrashLoop = %v want %v", c.name, got, c.want)
		}
	}
}

func TestCountSinceIncludesPendingAndFailed(t *testing.T) {
	restartedInit := corev1.ContainerStatus{
		RestartCount:         4,
		LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 1, FinishedAt: metav1.NewTime(t0.Add(-5 * time.Minute))}},
	}
	stuck := pod("a", corev1.PodPending, t0.Add(-time.Hour))
	stuck.Status.InitContainerStatuses = []corev1.ContainerStatus{restartedInit}
	src := &fakeSource{pods: map[string][]*corev1.Pod{
		SelectPending: {stuck},
		SelectFailed:  {pod("b", corev1.PodFailed, t0.Add(-time.Hour), restartedInit)},
	}}
	got, err := Count(context.Background(), src, Options{Now: t0, Since: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if got.RestartedInWindow != 2 {
		t.Errorf("RestartedInWindow = %d, want 2 (one Pending, one Failed)", got.RestartedInWindow)
	}
}

// partialTableSource emits rows and then reports ErrNoTable, as when a later
// page comes back as a plain list.
type partialTableSource struct{ *fakeSource }

func (p partialTableSource) Rows(_ context.Context, _ string, fn func(status, restarts string) error) error {
	_ = fn("Running", "0")
	_ = fn("CrashLoopBackOff", "3")
	return k8s.ErrNoTable
}

func TestCountFallbackDiscardsPartialRows(t *testing.T) {
	src := &fakeSource{pods: map[string][]*corev1.Pod{
		SelectRunning: {pod("x", corev1.PodRunning, t0)},
		SelectOther:   {pod("x", corev1.PodSucceeded, t0)},
	}}
	got, err := Count(context.Background(), partialTableSource{src}, Options{Now: t0})
	if err != nil {
		t.Fatal(err)
	}
	if got.Running != 1 || got.CrashLoop != 0 || got.Total != 2 {
		t.Errorf("rows before ErrNoTable must be discarded: %+v", got)
	}
}
