// Package fleet runs work in parallel across multiple Kubernetes contexts.
package fleet

import (
	"context"

	"golang.org/x/sync/errgroup"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
)

// ClusterResult is the per-context outcome of a parallel run.
type ClusterResult[T any] struct {
	Context string
	Value   T
	Err     error
}

// Run invokes fn once per context with bounded parallelism. It never fails
// fast: each context's error is captured in its ClusterResult.Err. The
// returned slice is ordered by the input refs slice.
//
// parallelism <= 0 means unbounded.
func Run[T any](ctx context.Context, refs []kubeconfig.ContextRef, parallelism int, fn func(context.Context, kubeconfig.ContextRef) (T, error)) []ClusterResult[T] {
	results := make([]ClusterResult[T], len(refs))
	g, gctx := errgroup.WithContext(ctx)
	if parallelism > 0 {
		g.SetLimit(parallelism)
	}
	for i, ref := range refs {
		i, ref := i, ref
		results[i].Context = ref.Name
		g.Go(func() error {
			v, err := fn(gctx, ref)
			results[i].Value = v
			results[i].Err = err
			return nil // never bubble; aggregation is in results
		})
	}
	_ = g.Wait()
	return results
}

// Stream is Run for large results: it hands each result to emit in refs
// order as soon as that context and every earlier one are done, then drops
// it, so at most the out-of-order backlog is held at once. emit runs on the
// caller's goroutine.
func Stream[T any](ctx context.Context, refs []kubeconfig.ContextRef, parallelism int, fn func(context.Context, kubeconfig.ContextRef) (T, error), emit func(ClusterResult[T])) {
	results := make([]ClusterResult[T], len(refs))
	done := make([]chan struct{}, len(refs))
	for i := range done {
		done[i] = make(chan struct{})
	}
	g, gctx := errgroup.WithContext(ctx)
	if parallelism > 0 {
		g.SetLimit(parallelism)
	}
	// g.Go blocks once the limit is reached, so launch from a goroutine and
	// keep this one free to emit.
	go func() {
		for i, ref := range refs {
			g.Go(func() error {
				v, err := fn(gctx, ref)
				results[i] = ClusterResult[T]{Context: ref.Name, Value: v, Err: err}
				close(done[i])
				return nil
			})
		}
	}()
	for i := range refs {
		<-done[i]
		emit(results[i])
		results[i] = ClusterResult[T]{}
	}
	_ = g.Wait()
}

// CountErrors returns the number of results with a non-nil Err.
func CountErrors[T any](results []ClusterResult[T]) int {
	n := 0
	for _, r := range results {
		if r.Err != nil {
			n++
		}
	}
	return n
}
