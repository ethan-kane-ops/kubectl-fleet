package fleet

import (
	"context"
	"errors"
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

func TestRunEmpty(t *testing.T) {
	got := Run(context.Background(), nil, 4,
		func(_ context.Context, _ kubeconfig.ContextRef) (int, error) { return 1, nil })
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}
