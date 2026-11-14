package pods

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/pager"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
)

var podsGVR = schema.GroupVersionResource{Version: "v1", Resource: "pods"}

// apiSource is a Source over a live API server: cells-only tables for Rows,
// the typed (protobuf) client for Pods.
type apiSource struct {
	tables *k8s.Tables
	cs     kubernetes.Interface
}

// NewSource returns a Source for the cluster behind restCfg and cs.
func NewSource(restCfg *rest.Config, cs kubernetes.Interface) (Source, error) {
	tables, err := k8s.NewTables(restCfg, podsGVR)
	if err != nil {
		return nil, err
	}
	return &apiSource{tables: tables.WithoutObjects(), cs: cs}, nil
}

func (s *apiSource) Rows(ctx context.Context, fieldSelector string, fn func(status, restarts string) error) error {
	return s.tables.List(ctx, "", metav1.ListOptions{FieldSelector: fieldSelector}, func(t *k8s.Table) error {
		status, restarts := -1, -1
		for i, c := range t.Columns {
			switch c.Name {
			case "STATUS":
				status = i
			case "RESTARTS":
				restarts = i
			}
		}
		switch {
		case len(t.Rows) == 0:
			return nil
		case status < 0:
			return fmt.Errorf("pod table has no STATUS column")
		case restarts < 0:
			return fmt.Errorf("pod table has no RESTARTS column")
		}
		for _, r := range t.Rows {
			if status >= len(r.Cells) || restarts >= len(r.Cells) {
				return fmt.Errorf("pod table row has %d cells, want at least %d", len(r.Cells), max(status, restarts)+1)
			}
			if err := fn(r.Cells[status], r.Cells[restarts]); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *apiSource) Pods(ctx context.Context, fieldSelector string, fn func(*corev1.Pod) error) error {
	lister := pager.New(func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
		return s.cs.CoreV1().Pods("").List(ctx, o)
	})
	return lister.EachListItem(ctx, metav1.ListOptions{FieldSelector: fieldSelector}, func(obj runtime.Object) error {
		p, ok := obj.(*corev1.Pod)
		if !ok {
			return fmt.Errorf("unexpected list item %T", obj)
		}
		return fn(p)
	})
}
