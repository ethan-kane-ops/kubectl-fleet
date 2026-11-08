package inventory

import (
	"context"
	"errors"
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func node(name, kubelet, os, kernel, runtime, arch string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{NodeInfo: corev1.NodeSystemInfo{
			KubeletVersion: kubelet, OSImage: os, KernelVersion: kernel,
			ContainerRuntimeVersion: runtime, Architecture: arch,
		}},
	}
}

func podSpec(image string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "c", Image: image}}}}
}

func fixture() *fake.Clientset {
	cs := fake.NewSimpleClientset(
		node("n1", "v1.30.4", "Bottlerocket OS 1.20.0", "6.1.90", "containerd://1.7.13", "arm64"),
		node("n2", "v1.30.4", "Bottlerocket OS 1.20.0", "6.1.90", "containerd://1.7.13", "arm64"),
		node("n3", "v1.29.8", "Bottlerocket OS 1.19.5", "6.1.80", "containerd://1.7.13", "amd64"),
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "coredns", Namespace: "kube-system"},
			Spec: appsv1.DeploymentSpec{Template: podSpec("registry.k8s.io/coredns/coredns:v1.11.1")}},
		&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "kube-proxy", Namespace: "kube-system"},
			Spec: appsv1.DaemonSetSpec{Template: podSpec("registry.k8s.io/kube-proxy:v1.30.4-eksbuild.1")}},
		&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "aws-node", Namespace: "kube-system"},
			Spec: appsv1.DaemonSetSpec{Template: podSpec("602401143452.dkr.ecr.eu-west-1.amazonaws.com/amazon-k8s-cni@sha256:0123456789abcdef0123456789abcdef")}},
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "elsewhere", Namespace: "default"},
			Spec: appsv1.DeploymentSpec{Template: podSpec("nginx:1.25")}},
	)
	cs.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{GitVersion: "v1.30.4-eks-a737599"}
	return cs
}

func TestCollect(t *testing.T) {
	cs := fixture()
	got, err := Collect(context.Background(), cs, cs.Discovery())
	if err != nil {
		t.Fatal(err)
	}
	want := Cluster{
		ControlPlane: "v1.30.4-eks-a737599",
		Kubelets:     []Count{{"v1.30.4", 2}, {"v1.29.8", 1}},
		OSImages:     []Count{{"Bottlerocket OS 1.20.0", 2}, {"Bottlerocket OS 1.19.5", 1}},
		Kernels:      []Count{{"6.1.90", 2}, {"6.1.80", 1}},
		Runtimes:     []Count{{"containerd://1.7.13", 3}},
		Arches:       []Count{{"arm64", 2}, {"amd64", 1}},
		Components: []Component{
			{Name: "aws-node", Kind: "DaemonSet", Version: "sha256:0123456789ab"},
			{Name: "coredns", Kind: "Deployment", Version: "v1.11.1"},
			{Name: "kube-proxy", Kind: "DaemonSet", Version: "v1.30.4-eksbuild.1"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestCollectForbiddenNodes(t *testing.T) {
	cs := fixture()
	cs.PrependReactor("list", "nodes", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "nodes"}, "", errors.New("rbac"))
	})
	got, err := Collect(context.Background(), cs, cs.Discovery())
	if err != nil {
		t.Fatalf("forbidden nodes should be partial, not an error: %v", err)
	}
	if !reflect.DeepEqual(got.Partial, []string{"nodes: forbidden"}) || len(got.Kubelets) != 0 || len(got.Components) != 3 {
		t.Errorf("partial=%v kubelets=%v components=%d", got.Partial, got.Kubelets, len(got.Components))
	}
}

func TestCollectOtherErrorsFail(t *testing.T) {
	cs := fixture()
	cs.PrependReactor("list", "daemonsets", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("connection reset")
	})
	if _, err := Collect(context.Background(), cs, cs.Discovery()); err == nil {
		t.Fatal("a non-RBAC error should fail the context")
	}
}

func TestImageVersion(t *testing.T) {
	cases := map[string]string{
		"registry.k8s.io/coredns/coredns:v1.11.1": "v1.11.1",
		"localhost:5000/team/app:2.0":             "2.0",
		"localhost:5000/team/app":                 "latest",
		"nginx":                                   "latest",
		"repo/app:1.2@sha256:abcdef0123456789aa":  "1.2",
		"repo/app@sha256:abcdef0123456789aa":      "sha256:abcdef012345",
	}
	for image, want := range cases {
		if got := imageVersion(image); got != want {
			t.Errorf("imageVersion(%q)=%q want %q", image, got, want)
		}
	}
}

func TestSpread(t *testing.T) {
	clusters := map[string]Cluster{
		"a": {ControlPlane: "v1.30.4", Kubelets: []Count{{"v1.30.4", 3}, {"v1.29.8", 1}}, OSImages: []Count{{"Bottlerocket 1.20", 4}},
			Components: []Component{{Name: "coredns", Kind: "Deployment", Version: "v1.11.1"}}},
		"b": {ControlPlane: "v1.30.4", Kubelets: []Count{{"v1.30.4", 2}}, OSImages: []Count{{"Bottlerocket 1.20", 2}},
			Components: []Component{{Name: "coredns", Kind: "Deployment", Version: "v1.10.1"}}},
		"c": {ControlPlane: "v1.29.8", Kubelets: []Count{{"v1.29.8", 5}},
			Components: []Component{{Name: "coredns", Kind: "Deployment", Version: "v1.11.1"}, {Name: "aws-node", Kind: "DaemonSet", Version: "v1.18.0"}}},
	}
	want := []SpreadRow{
		{"kube-apiserver", "v1.30.4", 2},
		{"kube-apiserver", "v1.29.8", 1},
		{"kubelet", "v1.29.8", 2},
		{"kubelet", "v1.30.4", 2},
		{"os-image", "Bottlerocket 1.20", 2},
		{"aws-node", "v1.18.0", 1},
		{"coredns", "v1.11.1", 2},
		{"coredns", "v1.10.1", 1},
	}
	if got := Spread(clusters); !reflect.DeepEqual(got, want) {
		t.Errorf("got  %v\nwant %v", got, want)
	}
}
