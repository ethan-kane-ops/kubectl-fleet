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
			return nil // never bubble — aggregation is in results
		})
	}
	_ = g.Wait()
	return results
}
