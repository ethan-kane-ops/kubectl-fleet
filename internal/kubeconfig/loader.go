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

// RESTConfigFor returns a REST config for a specific context, honoring all
// kubectl ConfigFlags except the global --context (which is overridden).
func RESTConfigFor(kubeFlags *genericclioptions.ConfigFlags, ctxName string) (*rest.Config, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeFlags != nil && kubeFlags.KubeConfig != nil && *kubeFlags.KubeConfig != "" {
		rules.ExplicitPath = *kubeFlags.KubeConfig
	}
	overrides := &clientcmd.ConfigOverrides{CurrentContext: ctxName}
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides)
	restCfg, err := cc.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("rest config for context %q: %w", ctxName, err)
	}
	return restCfg, nil
}
