// Package kubeconfig loads kubeconfig and resolves per-context REST configs.
package kubeconfig

import (
	"fmt"
	"regexp"
	"sort"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// ContextRef is a flattened view of a single kubeconfig context.
type ContextRef struct {
	Name      string
	Cluster   string
	Namespace string
	AuthInfo  string
}

// Load returns the merged kubeconfig respecting --kubeconfig + KUBECONFIG env.
func Load(kubeFlags *genericclioptions.ConfigFlags) (*clientcmdapi.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeFlags != nil && kubeFlags.KubeConfig != nil && *kubeFlags.KubeConfig != "" {
		rules.ExplicitPath = *kubeFlags.KubeConfig
	}
	cfg, err := rules.Load()
	if err != nil {
		return nil, fmt.Errorf("load kubeconfig: %w", err)
	}
	return cfg, nil
}

// Contexts returns contexts from cfg sorted by name, optionally filtered by a
// regex matching context name. Empty filter returns all.
func Contexts(cfg *clientcmdapi.Config, filter string) ([]ContextRef, error) {
	var re *regexp.Regexp
	if filter != "" {
		var err error
		re, err = regexp.Compile(filter)
		if err != nil {
			return nil, fmt.Errorf("compile context filter %q: %w", filter, err)
		}
	}
	out := make([]ContextRef, 0, len(cfg.Contexts))
	for name, c := range cfg.Contexts {
		if re != nil && !re.MatchString(name) {
			continue
		}
		out = append(out, ContextRef{
			Name:      name,
			Cluster:   c.Cluster,
			Namespace: c.Namespace,
			AuthInfo:  c.AuthInfo,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Select returns the contexts a command runs against: only --context when it
// is set (overriding filter), otherwise Contexts(cfg, filter).
func Select(kubeFlags *genericclioptions.ConfigFlags, cfg *clientcmdapi.Config, filter string) ([]ContextRef, error) {
	if kubeFlags == nil || kubeFlags.Context == nil || *kubeFlags.Context == "" {
		return Contexts(cfg, filter)
	}
	name := *kubeFlags.Context
	c, ok := cfg.Contexts[name]
	if !ok {
		return nil, fmt.Errorf("context %q not found in kubeconfig", name)
	}
	return []ContextRef{{Name: name, Cluster: c.Cluster, Namespace: c.Namespace, AuthInfo: c.AuthInfo}}, nil
}

// ForContext returns ConfigFlags pinned to ctxName that carry every global
// kubectl flag the user set (--as, --token, --request-timeout, --cache-dir,
// ...). The copy is non-persistent so each context resolves its own client
// config, discovery cache and namespace.
func ForContext(kubeFlags *genericclioptions.ConfigFlags, ctxName string) *genericclioptions.ConfigFlags {
	f := genericclioptions.NewConfigFlags(false)
	if kubeFlags != nil {
		f.CacheDir = kubeFlags.CacheDir
		f.KubeConfig = kubeFlags.KubeConfig
		f.Namespace = kubeFlags.Namespace
		f.Insecure = kubeFlags.Insecure
		f.CertFile = kubeFlags.CertFile
		f.KeyFile = kubeFlags.KeyFile
		f.CAFile = kubeFlags.CAFile
		f.BearerToken = kubeFlags.BearerToken
		f.Impersonate = kubeFlags.Impersonate
		f.ImpersonateUID = kubeFlags.ImpersonateUID
		f.ImpersonateGroup = kubeFlags.ImpersonateGroup
		f.Username = kubeFlags.Username
		f.Password = kubeFlags.Password
		f.Timeout = kubeFlags.Timeout
		f.DisableCompression = kubeFlags.DisableCompression
		f.WrapConfigFn = kubeFlags.WrapConfigFn
	}
	f.Context = &ctxName
	return f
}

// ValidateFleetFlags rejects global kubectl flags that pin a single cluster
// or user. Applied across a fleet they would point every context at
// the same target.
func ValidateFleetFlags(kubeFlags *genericclioptions.ConfigFlags) error {
	if kubeFlags == nil {
		return nil
	}
	single := []struct {
		flag string
		val  *string
		hint string
	}{
		{"--cluster", kubeFlags.ClusterName, "it would point every context at one cluster"},
		{"--server", kubeFlags.APIServer, "it would point every context at one API server"},
		{"--tls-server-name", kubeFlags.TLSServerName, "it would apply one server name to every cluster"},
		{"--user", kubeFlags.AuthInfoName, "it would use one kubeconfig user for every context"},
	}
	for _, s := range single {
		if s.val != nil && *s.val != "" {
			return fmt.Errorf("%s is not supported by kubectl fleet: %s", s.flag, s.hint)
		}
	}
	return nil
}

// RESTConfigFor returns a REST config for a specific context, honoring the
// global kubectl flags as ForContext describes.
func RESTConfigFor(kubeFlags *genericclioptions.ConfigFlags, ctxName string) (*rest.Config, error) {
	restCfg, err := ForContext(kubeFlags, ctxName).ToRESTConfig()
	if err != nil {
		return nil, fmt.Errorf("rest config for context %q: %w", ctxName, err)
	}
	return restCfg, nil
}

// NamespaceFor returns the namespace a namespaced request against ctxName
// should use: --namespace, else the context's namespace, else "default".
func NamespaceFor(kubeFlags *genericclioptions.ConfigFlags, ctxName string) (string, error) {
	ns, _, err := ForContext(kubeFlags, ctxName).ToRawKubeConfigLoader().Namespace()
	if err != nil {
		return "", fmt.Errorf("namespace for context %q: %w", ctxName, err)
	}
	return ns, nil
}
