// Package printers maps Kubernetes resource kinds to per-kind column schemas
// for the `get` subcommand. Each schema knows its column headers and how to
// project an unstructured object into a row.
package printers

import (
	"fmt"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// RowFn returns the regular columns and wide columns for a single resource.
// Returned slices match the lengths of Schema.Headers and Schema.WideHeaders.
type RowFn func(u *unstructured.Unstructured) (cols, wide []string)

// Schema describes the per-kind column layout used by `kubectl fleet get`.
// Headers and WideHeaders exclude the leading CONTEXT and (optional)
// NAMESPACE columns — those are added by the caller.
type Schema struct {
	Headers     []string
	WideHeaders []string
	Row         RowFn
	// Namespaced declares whether the kind is namespace-scoped. Callers use
	// this as a fallback when no cluster discovery succeeded (e.g., every
	// cluster errored) to decide whether to render the NAMESPACE column.
	Namespaced bool
}

var registry = map[string]Schema{}

// register adds a schema keyed by every alias provided (typically the GVR
// resource plural plus singular and short names).
func register(s Schema, aliases ...string) {
	for _, a := range aliases {
		registry[strings.ToLower(a)] = s
	}
}

// For returns the schema registered under any of: GVR resource plural, the
// kind singular, or a short name. The boolean is false when no per-kind
// schema is registered — callers should fall back to Generic().
func For(name string) (Schema, bool) {
	s, ok := registry[strings.ToLower(strings.TrimSpace(name))]
	return s, ok
}

// Generic returns the default schema used when no per-kind schema exists.
// Two columns: NAME and AGE, with KIND / API_VERSION shown in wide mode.
// Namespaced defaults to true so unknown CRDs render the NAMESPACE column;
// callers override based on cluster discovery when it succeeds.
func Generic() Schema {
	return Schema{
		Headers:     []string{"NAME", "AGE"},
		WideHeaders: []string{"KIND", "API_VERSION"},
		Namespaced:  true,
		Row: func(u *unstructured.Unstructured) ([]string, []string) {
			return []string{u.GetName(), Age(u.GetCreationTimestamp())},
				[]string{u.GetKind(), u.GetAPIVersion()}
		},
	}
}

// Age renders a metav1.Time as a short human duration ("5m", "3h", "12d").
// Empty timestamps render as the empty string.
func Age(ts metav1.Time) string {
	if ts.IsZero() {
		return ""
	}
	d := time.Since(ts.Time)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func intFromObj(obj map[string]interface{}, path ...string) int64 {
	v, found, err := unstructured.NestedInt64(obj, path...)
	if err != nil || !found {
		return 0
	}
	return v
}

func stringFromObj(obj map[string]interface{}, path ...string) string {
	v, found, err := unstructured.NestedString(obj, path...)
	if err != nil || !found {
		return ""
	}
	return v
}

func sliceFromObj(obj map[string]interface{}, path ...string) []interface{} {
	v, found, err := unstructured.NestedSlice(obj, path...)
	if err != nil || !found {
		return nil
	}
	return v
}

// asMap safely casts an interface{} (typically a slice element) to a map.
func asMap(v interface{}) map[string]interface{} {
	m, _ := v.(map[string]interface{})
	return m
}
