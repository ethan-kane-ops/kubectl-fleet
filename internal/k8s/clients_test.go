package k8s

import (
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
		in       string
		wantRes  string
		wantGrp  string
		wantNsd  bool
		wantOK   bool
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
