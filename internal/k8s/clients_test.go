package k8s

import (
	"errors"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
)

func fakeDisco() discovery.DiscoveryInterface {
	cs := fake.NewSimpleClientset()
	fd, _ := cs.Discovery().(*fakediscovery.FakeDiscovery)
	fd.Resources = []*metav1.APIResourceList{
		{
			GroupVersion: "v1",
			APIResources: []metav1.APIResource{
				{Name: "pods", SingularName: "pod", Namespaced: true, Kind: "Pod", ShortNames: []string{"po"}},
				{Name: "namespaces", SingularName: "namespace", Namespaced: false, Kind: "Namespace", ShortNames: []string{"ns"}},
				{Name: "nodes", SingularName: "node", Namespaced: false, Kind: "Node", ShortNames: []string{"no"}},
			},
		},
		{
			GroupVersion: "apps/v1",
			APIResources: []metav1.APIResource{
				{Name: "deployments", SingularName: "deployment", Namespaced: true, Kind: "Deployment", ShortNames: []string{"deploy"}},
			},
		},
	}
	return cs.Discovery()
}

func TestResolveGVR(t *testing.T) {
	disco := fakeDisco()
	cases := []struct {
		in      string
		wantRes string
		wantGrp string
		wantNsd bool
		wantOK  bool
	}{
		{"pods", "pods", "", true, true},
		{"pod", "pods", "", true, true},
		{"po", "pods", "", true, true},
		{"Pod", "pods", "", true, true},
		{"deployments", "deployments", "apps", true, true},
		{"deploy", "deployments", "apps", true, true},
		{"namespaces", "namespaces", "", false, true},
		{"ns", "namespaces", "", false, true},
		{"nonsense", "", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			gvr, nsd, err := ResolveGVR(disco, c.in)
			if c.wantOK && err != nil {
				t.Fatalf("ResolveGVR(%q): %v", c.in, err)
			}
			if !c.wantOK && err == nil {
				t.Fatalf("ResolveGVR(%q) expected err", c.in)
			}
			if !c.wantOK {
				return
			}
			if gvr.Resource != c.wantRes {
				t.Errorf("resource=%q want %q", gvr.Resource, c.wantRes)
			}
			if gvr.Group != c.wantGrp {
				t.Errorf("group=%q want %q", gvr.Group, c.wantGrp)
			}
			if nsd != c.wantNsd {
				t.Errorf("namespaced=%v want %v", nsd, c.wantNsd)
			}
		})
	}
}

// staleCache wraps a discovery client whose first view lacks a resource,
// standing in for a disk cache written before a CRD was installed.
type staleCache struct {
	discovery.DiscoveryInterface
	fresh       bool
	invalidated int
	afterReload []*metav1.APIResourceList
}

func (s *staleCache) Fresh() bool { return s.fresh }

func (s *staleCache) Invalidate() {
	s.invalidated++
	s.fresh = true
	s.DiscoveryInterface.(*fakediscovery.FakeDiscovery).Resources = s.afterReload
}

func TestResolveGVRInvalidatesStaleCache(t *testing.T) {
	widgets := &metav1.APIResourceList{
		GroupVersion: "example.io/v1",
		APIResources: []metav1.APIResource{{Name: "widgets", SingularName: "widget", Namespaced: true, Kind: "Widget"}},
	}
	cases := []struct {
		name            string
		fresh           bool
		wantErr         bool
		wantInvalidated int
	}{
		{"stale cache is invalidated and retried", false, false, 1},
		{"fresh cache is trusted", true, true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fd := fakeDisco()
			reload := append(append([]*metav1.APIResourceList{}, fd.(*fakediscovery.FakeDiscovery).Resources...), widgets)
			c := &staleCache{DiscoveryInterface: fd, fresh: tc.fresh, afterReload: reload}
			gvr, _, err := ResolveGVR(c, "widgets")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && gvr.Group != "example.io" {
				t.Errorf("gvr=%v", gvr)
			}
			if c.invalidated != tc.wantInvalidated {
				t.Errorf("invalidated=%d want %d", c.invalidated, tc.wantInvalidated)
			}
		})
	}
}

// partialDisco returns the fake's resources together with a discovery error,
// as client-go does when one API group (e.g. a down aggregated API) fails.
type partialDisco struct{ discovery.DiscoveryInterface }

func (p partialDisco) ServerGroupsAndResources() ([]*metav1.APIGroup, []*metav1.APIResourceList, error) {
	g, r, _ := p.DiscoveryInterface.ServerGroupsAndResources()
	return g, r, errors.New("metrics.k8s.io/v1beta1: the server is currently unable to handle the request")
}

func TestResolveGVRPartialDiscovery(t *testing.T) {
	d := partialDisco{fakeDisco()}
	if _, _, err := ResolveGVR(d, "pods"); err != nil {
		t.Fatalf("a kind in a healthy group should resolve despite the partial failure: %v", err)
	}
	_, _, err := ResolveGVR(d, "podmetrics")
	if err == nil || !strings.Contains(err.Error(), "metrics.k8s.io/v1beta1") {
		t.Fatalf("want the discovery failure in the error, got %v", err)
	}
	if !errors.Is(err, ErrKindNotFound) {
		t.Errorf("error should still wrap ErrKindNotFound: %v", err)
	}
}
