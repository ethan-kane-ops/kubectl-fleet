package cmd

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/metadata"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
	"github.com/ethan-kane-ops/kubectl-fleet/internal/kubeconfig"
)

// completionDeadline bounds every completion that calls clusters. Whatever
// answered by then is offered; slow or dead clusters just don't contribute,
// so a large or partly dead fleet never blocks the shell.
const completionDeadline = 2500 * time.Millisecond

// completionParallelism caps concurrent cluster calls during completion.
const completionParallelism = 32

const noFiles = cobra.ShellCompDirectiveNoFileComp

// registerCompletions wires flag completion on root and every subcommand:
// context names for --context, --contexts, --exclude and --filter, and the
// fixed values of -o, --sort-by and --where.
func registerCompletions(root *cobra.Command, kubeFlags *genericclioptions.ConfigFlags) {
	contextNames := func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		cfg, err := kubeconfig.Load(kubeFlags)
		if err != nil {
			return nil, noFiles
		}
		refs, _ := kubeconfig.Contexts(cfg, "")
		names := make([]string, 0, len(refs))
		for _, r := range refs {
			names = append(names, r.Name)
		}
		return names, noFiles
	}
	_ = root.RegisterFlagCompletionFunc("context", contextNames)
	fixed := map[string][]string{
		"output":  {"table", "wide", "json", "yaml", "name"},
		"sort-by": statusSortKeys,
		"where":   {"version<", "version>=", "nodes>", "nodes<", "reachable", "!reachable"},
	}
	for _, c := range root.Commands() {
		for _, name := range []string{"contexts", "exclude", "filter"} {
			if c.Flags().Lookup(name) != nil {
				_ = c.RegisterFlagCompletionFunc(name, contextNames)
			}
		}
		for name, values := range fixed {
			if c.Flags().Lookup(name) == nil {
				continue
			}
			directive := noFiles
			if name == "where" {
				// version< and friends need a value typed after them.
				directive |= cobra.ShellCompDirectiveNoSpace
			}
			_ = c.RegisterFlagCompletionFunc(name, cobra.FixedCompletions(values, directive))
		}
	}
}

// completionContexts is what completion asks: the contexts selected by flags
// already typed, else the current context.
func completionContexts(kubeFlags *genericclioptions.ConfigFlags, o *fleetOptions) (*clientcmdapi.Config, []kubeconfig.ContextRef) {
	cfg, err := kubeconfig.Load(kubeFlags)
	if err != nil {
		return nil, nil
	}
	if (kubeFlags.Context == nil || *kubeFlags.Context == "") && o.contexts == "" && o.exclude == "" {
		if cfg.CurrentContext == "" {
			return cfg, nil
		}
		ctx := cfg.Contexts[cfg.CurrentContext]
		if ctx == nil {
			return cfg, nil
		}
		return cfg, []kubeconfig.ContextRef{{Name: cfg.CurrentContext, Cluster: ctx.Cluster, Namespace: ctx.Namespace, AuthInfo: ctx.AuthInfo}}
	}
	refs, err := kubeconfig.Select(kubeFlags, cfg, o.contexts, o.exclude)
	if err != nil {
		return cfg, nil
	}
	return cfg, refs
}

// gather calls fn for each context in parallel and returns the union of
// whatever finished before completionDeadline, sorted and de-duplicated.
// Calls still running are abandoned: completion is a short-lived process.
func gather(refs []kubeconfig.ContextRef, fn func(context.Context, kubeconfig.ContextRef) []string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), completionDeadline)
	defer cancel()
	results := make(chan []string, len(refs))
	sem := make(chan struct{}, completionParallelism)
	for _, r := range refs {
		go func() {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
				results <- fn(ctx, r)
			case <-ctx.Done():
				results <- nil
			}
		}()
	}
	seen := map[string]bool{}
	for range refs {
		select {
		case names := <-results:
			for _, n := range names {
				seen[n] = true
			}
		case <-ctx.Done():
			return sortedKeys(seen)
		}
	}
	return sortedKeys(seen)
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// completeKinds offers listable resource names from each selected context's
// discovery (disk-cached, like kubectl). A plural served by several groups
// is also offered as plural.group.
func completeKinds(kubeFlags *genericclioptions.ConfigFlags, o *fleetOptions) []string {
	cfg, refs := completionContexts(kubeFlags, o)
	return qualifyKinds(gather(refs, func(_ context.Context, r kubeconfig.ContextRef) []string {
		disco, err := kubeconfig.ForContext(kubeFlags, cfg, r.Name).ToDiscoveryClient()
		if err != nil {
			return nil
		}
		_, lists, _ := disco.ServerGroupsAndResources()
		var out []string
		for _, l := range lists {
			gv, err := schema.ParseGroupVersion(l.GroupVersion)
			if err != nil {
				continue
			}
			for _, res := range l.APIResources {
				if strings.Contains(res.Name, "/") || !hasVerb(res.Verbs, "list") {
					continue
				}
				out = append(out, res.Name+"\x00"+gv.Group)
			}
		}
		return out
	}))
}

// qualifyKinds turns "plural\x00group" entries into completion candidates.
func qualifyKinds(entries []string) []string {
	groups := map[string]map[string]bool{}
	for _, e := range entries {
		plural, group, _ := strings.Cut(e, "\x00")
		if groups[plural] == nil {
			groups[plural] = map[string]bool{}
		}
		groups[plural][group] = true
	}
	seen := map[string]bool{}
	for plural, gs := range groups {
		seen[plural] = true
		if len(gs) > 1 {
			for g := range gs {
				if g != "" {
					seen[plural+"."+g] = true
				}
			}
		}
	}
	return sortedKeys(seen)
}

func hasVerb(verbs metav1.Verbs, verb string) bool {
	for _, v := range verbs {
		if v == verb {
			return true
		}
	}
	return false
}

// completeNames offers object names of kind from the selected contexts (or
// the current one), metadata only, one page per context.
func completeNames(kubeFlags *genericclioptions.ConfigFlags, o *fleetOptions, kind string, allNamespaces bool) []string {
	cfg, refs := completionContexts(kubeFlags, o)
	return gather(refs, func(ctx context.Context, r kubeconfig.ContextRef) []string {
		kc := kubeconfig.ForContext(kubeFlags, cfg, r.Name)
		disco, err := kc.ToDiscoveryClient()
		if err != nil {
			return nil
		}
		gvr, namespaced, err := k8s.ResolveGVR(disco, kind)
		if err != nil {
			return nil
		}
		ns := ""
		if namespaced && !allNamespaces {
			if ns, err = kc.Namespace(); err != nil {
				return nil
			}
		}
		restCfg, err := kc.ToRESTConfig()
		if err != nil {
			return nil
		}
		restCfg.Timeout = completionDeadline
		mc, err := metadata.NewForConfig(restCfg)
		if err != nil {
			return nil
		}
		list, err := mc.Resource(gvr).Namespace(ns).List(ctx, metav1.ListOptions{Limit: 500})
		if err != nil {
			return nil
		}
		names := make([]string, 0, len(list.Items))
		for _, item := range list.Items {
			names = append(names, item.Name)
		}
		return names
	})
}
