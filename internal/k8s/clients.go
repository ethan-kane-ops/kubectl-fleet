// Package k8s builds typed/dynamic/discovery clients from a REST config and
// resolves Kubernetes kind shorthands to GroupVersionResource.
package k8s

import (
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

// Clients bundles the three flavours of client kubectl-fleet uses.
type Clients struct {
	Typed     kubernetes.Interface
	Dynamic   dynamic.Interface
	Discovery discovery.DiscoveryInterface
}

// New builds a Clients from a REST client getter, typically the per-context
// ConfigFlags from kubeconfig.ForContext. Discovery goes through the getter's
// disk-cached client, the same cache and rate limits kubectl uses.
func New(getter genericclioptions.RESTClientGetter) (*Clients, error) {
	restCfg, err := getter.ToRESTConfig()
	if err != nil {
		return nil, fmt.Errorf("rest config: %w", err)
	}
	typed, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("typed client: %w", err)
	}
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, fmt.Errorf("dynamic client: %w", err)
	}
	disco, err := getter.ToDiscoveryClient()
	if err != nil {
		return nil, fmt.Errorf("discovery client: %w", err)
	}
	return &Clients{Typed: typed, Dynamic: dyn, Discovery: disco}, nil
}

// ResolveGVR maps a user-supplied kind shorthand to a GroupVersionResource by
// scanning ServerGroupsAndResources.
//
// Accepts: resource plural ("pods"), singular ("pod"), short ("po"), kind
// ("Pod"), or group-qualified plural ("deployments.apps").
//
// When disco is a stale cache and the kind is missing, the cache is
// invalidated and the lookup retried once, so a newly installed CRD resolves
// without waiting for the cache to expire.
func ResolveGVR(disco discovery.DiscoveryInterface, kind string) (gvr schema.GroupVersionResource, namespaced bool, err error) {
	gvr, namespaced, err = resolveGVR(disco, kind)
	if errors.Is(err, errKindNotFound) {
		if cached, ok := disco.(discovery.CachedDiscoveryInterface); ok && !cached.Fresh() {
			cached.Invalidate()
			return resolveGVR(disco, kind)
		}
	}
	return gvr, namespaced, err
}

var errKindNotFound = errors.New("not found in cluster")

func resolveGVR(disco discovery.DiscoveryInterface, kind string) (gvr schema.GroupVersionResource, namespaced bool, err error) {
	gr := schema.ParseGroupResource(kind)
	target := strings.ToLower(gr.Resource)
	wantGroup := gr.Group

	_, resources, listErr := disco.ServerGroupsAndResources()
	if listErr != nil && len(resources) == 0 {
		return schema.GroupVersionResource{}, false, fmt.Errorf("discover resources: %w", listErr)
	}

	var best *match
	for _, list := range resources {
		gv, perr := schema.ParseGroupVersion(list.GroupVersion)
		if perr != nil {
			continue
		}
		if wantGroup != "" && wantGroup != gv.Group {
			continue
		}
		for _, r := range list.APIResources {
			if strings.Contains(r.Name, "/") {
				continue
			}
			m := scoreMatch(r, gv, target)
			if m == nil {
				continue
			}
			if best == nil || m.score > best.score {
				best = m
			}
		}
	}
	if best == nil {
		// With partial discovery the kind may live in a group that failed
		// (a down aggregated API), so the discovery error is the real cause.
		if listErr != nil {
			return schema.GroupVersionResource{}, false, fmt.Errorf("kind %q %w (discovery incomplete: %w)", kind, errKindNotFound, listErr)
		}
		return schema.GroupVersionResource{}, false, fmt.Errorf("kind %q %w", kind, errKindNotFound)
	}
	return best.gvr, best.namespaced, nil
}

type match struct {
	gvr        schema.GroupVersionResource
	namespaced bool
	score      int
}

// scoreMatch prefers exact plural > singular > kind > shortName. Higher is
// better. nil = no match.
func scoreMatch(r metav1.APIResource, gv schema.GroupVersion, target string) *match {
	score := 0
	switch {
	case strings.EqualFold(r.Name, target):
		score = 4
	case strings.EqualFold(r.SingularName, target):
		score = 3
	case strings.EqualFold(r.Kind, target):
		score = 2
	default:
		for _, sn := range r.ShortNames {
			if strings.EqualFold(sn, target) {
				score = 1
				break
			}
		}
	}
	if score == 0 {
		return nil
	}
	if gv.Group == "" {
		score += 10
	}
	return &match{gvr: gv.WithResource(r.Name), namespaced: r.Namespaced, score: score}
}
