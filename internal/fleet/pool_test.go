package fleet

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
)

func refs(names ...string) []kubeconfig.ContextRef {
	out := make([]kubeconfig.ContextRef, len(names))
	for i, n := range names {
		out[i] = kubeconfig.ContextRef{Name: n}
	}
	return out
}

func TestRunPreservesOrder(t *testing.T) {
	got := Run(context.Background(), refs("a", "b", "c"), 8,
		func(_ context.Context, r kubeconfig.ContextRef) (string, error) {
			return r.Name + "!", nil
		})
	want := []string{"a!", "b!", "c!"}
	for i, r := range got {
		if r.Err != nil {
			t.Errorf("[%d] err=%v", i, r.Err)
		}
		if r.Value != want[i] {
			t.Errorf("[%d] value=%q want %q", i, r.Value, want[i])
		}
		if r.Context != refs("a", "b", "c")[i].Name {
			t.Errorf("[%d] context=%q", i, r.Context)
		}
	}
}

func TestRunAggregatesPerCtxError(t *testing.T) {
	boom := errors.New("boom")
	got := Run(context.Background(), refs("ok1", "bad", "ok2"), 8,
		func(_ context.Context, r kubeconfig.ContextRef) (int, error) {
			if r.Name == "bad" {
				return 0, boom
			}
			return 42, nil
		})
	if got[0].Err != nil || got[2].Err != nil {
		t.Errorf("ok contexts errored: %+v", got)
	}
	if !errors.Is(got[1].Err, boom) {
		t.Errorf("bad ctx err=%v want %v", got[1].Err, boom)
	}
	if got[0].Value != 42 || got[2].Value != 42 {
		t.Errorf("values: %+v", got)
	}
}

func TestRunRespectsParallelismLimit(t *testing.T) {
	var inflight, peak int32
	rs := refs("a", "b", "c", "d", "e", "f", "g", "h")
	_ = Run(context.Background(), rs, 3,
		func(_ context.Context, _ kubeconfig.ContextRef) (struct{}, error) {
			n := atomic.AddInt32(&inflight, 1)
			defer atomic.AddInt32(&inflight, -1)
			for {
				p := atomic.LoadInt32(&peak)
				if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			return struct{}{}, nil
		})
	if peak > 3 {
		t.Errorf("peak inflight=%d want <=3", peak)
	}
}

func TestCountErrors(t *testing.T) {
	boom := errors.New("boom")
	got := Run(context.Background(), refs("ok1", "bad1", "bad2"), 8,
		func(_ context.Context, r kubeconfig.ContextRef) (int, error) {
			if r.Name == "ok1" {
				return 42, nil
			}
			return 0, boom
		})
	if n := CountErrors(got); n != 2 {
		t.Errorf("CountErrors = %d, want 2", n)
	}
}

func TestRunEmpty(t *testing.T) {
	got := Run(context.Background(), nil, 4,
		func(_ context.Context, _ kubeconfig.ContextRef) (int, error) { return 1, nil })
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

func TestStream(t *testing.T) {
	// Later contexts finish first; emit must still see refs order, and each
	// result as soon as its predecessors are done.
	delays := map[string]time.Duration{"a": 60 * time.Millisecond, "b": 20 * time.Millisecond, "c": 0, "d": 40 * time.Millisecond}
	cases := []struct {
		name        string
		parallelism int
	}{{"bounded", 2}, {"unbounded", 0}, {"serial", 1}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var inFlight, maxInFlight atomic.Int32
			var emitted []string
			failed := 0
			Stream(context.Background(), refs("a", "b", "c", "d"), tc.parallelism,
				func(_ context.Context, r kubeconfig.ContextRef) (string, error) {
					n := inFlight.Add(1)
					defer inFlight.Add(-1)
					for {
						m := maxInFlight.Load()
						if n <= m || maxInFlight.CompareAndSwap(m, n) {
							break
						}
					}
					time.Sleep(delays[r.Name])
					if r.Name == "c" {
						return "", errors.New("boom")
					}
					return r.Name + "!", nil
				},
				func(r ClusterResult[string]) {
					emitted = append(emitted, r.Context+"="+r.Value)
					if r.Err != nil {
						failed++
					}
				})
			if got := strings.Join(emitted, ","); got != "a=a!,b=b!,c=,d=d!" {
				t.Errorf("emitted %s", got)
			}
			if failed != 1 {
				t.Errorf("failed=%d want 1", failed)
			}
			if tc.parallelism > 0 && int(maxInFlight.Load()) > tc.parallelism {
				t.Errorf("max in flight %d exceeds %d", maxInFlight.Load(), tc.parallelism)
			}
		})
	}
}

func TestStreamEmpty(t *testing.T) {
	Stream(context.Background(), nil, 4,
		func(context.Context, kubeconfig.ContextRef) (int, error) { return 0, nil },
		func(ClusterResult[int]) { t.Error("emit called for no contexts") })
}
