// Package kubeconfig loads kubeconfig and resolves per-context REST configs.
package kubeconfig

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"k8s.io/cli-runtime/pkg/genericclioptions"
	"k8s.io/client-go/discovery"
	diskcached "k8s.io/client-go/discovery/cached/disk"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/util/homedir"
)

// ContextRef is a flattened view of a single kubeconfig context.
type ContextRef struct {
	Name      string
	Cluster   string
	Namespace string
	AuthInfo  string
}

// Load returns the merged kubeconfig respecting --kubeconfig + KUBECONFIG env.
// It is the only place the kubeconfig is parsed; ForContext works from it.
func Load(kubeFlags *genericclioptions.ConfigFlags) (*clientcmdapi.Config, error) {
	cfg, err := loadingRules(kubeFlags).Load()
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

// Client-side rate limits per context. Discovery keeps kubectl's burst of
// 300.
const (
	requestQPS     = 50
	requestBurst   = 100
	discoveryQPS   = 50
	discoveryBurst = 300
	discoveryTTL   = 6 * time.Hour
)

// Context builds clients for one kubeconfig context from the kubeconfig
// parsed once by Load, so a fleet of N contexts costs one parse, not N. It
// applies every global kubectl flag the user set (--as, --request-timeout,
// --cache-dir, ...) and is safe for concurrent use.
type Context struct {
	name     string
	cc       clientcmd.ClientConfig
	cacheDir string
	wrap     func(*rest.Config) *rest.Config

	discoOnce sync.Once
	disco     discovery.CachedDiscoveryInterface
	discoErr  error
}

// ForContext returns the Context for ctxName in cfg (from Load).
func ForContext(kubeFlags *genericclioptions.ConfigFlags, cfg *clientcmdapi.Config, ctxName string) *Context {
	rules := loadingRules(kubeFlags)
	overrides := overridesFrom(kubeFlags)
	overrides.CurrentContext = ctxName
	c := &Context{
		name: ctxName,
		// rules is only the ConfigAccess that persists refreshed auth-provider
		// tokens; nothing is re-read from disk.
		cc:       clientcmd.NewNonInteractiveClientConfig(*cfg, ctxName, overrides, rules),
		cacheDir: defaultCacheDir(),
	}
	if kubeFlags != nil {
		if kubeFlags.CacheDir != nil && *kubeFlags.CacheDir != "" {
			c.cacheDir = *kubeFlags.CacheDir
		}
		c.wrap = kubeFlags.WrapConfigFn
	}
	return c
}

// Name is the kubeconfig context name.
func (c *Context) Name() string { return c.name }

// ToRESTConfig returns a REST config for the context.
func (c *Context) ToRESTConfig() (*rest.Config, error) {
	rc, err := c.cc.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("rest config for context %q: %w", c.name, err)
	}
	if c.wrap != nil {
		rc = c.wrap(rc)
	}
	rc.WarningHandler = serverWarnings.forContext(c.name)
	// client-go defaults to 5 QPS / burst 10, which throttles paging a large
	// cluster (100 pages for 50k pods). The limiter is per context, so a
	// higher rate does not multiply across the fleet.
	if rc.QPS == 0 {
		rc.QPS, rc.Burst = requestQPS, requestBurst
	}
	return rc, nil
}

// ToDiscoveryClient returns the context's disk-cached discovery client, in
// the same cache directory kubectl uses, built once per Context.
func (c *Context) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	c.discoOnce.Do(func() {
		rc, err := c.ToRESTConfig()
		if err != nil {
			c.discoErr = err
			return
		}
		rc.QPS, rc.Burst = discoveryQPS, discoveryBurst
		c.disco, c.discoErr = diskcached.NewCachedDiscoveryClientForConfig(rc,
			discoveryCacheDir(filepath.Join(c.cacheDir, "discovery"), rc.Host),
			filepath.Join(c.cacheDir, "http"), discoveryTTL)
	})
	return c.disco, c.discoErr
}

// Namespace returns the namespace a namespaced request should use:
// --namespace, else the context's namespace, else "default".
func (c *Context) Namespace() (string, error) {
	ns, _, err := c.cc.Namespace()
	if err != nil {
		return "", fmt.Errorf("namespace for context %q: %w", c.name, err)
	}
	return ns, nil
}

func loadingRules(kubeFlags *genericclioptions.ConfigFlags) *clientcmd.ClientConfigLoadingRules {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if kubeFlags != nil && kubeFlags.KubeConfig != nil && *kubeFlags.KubeConfig != "" {
		rules.ExplicitPath = *kubeFlags.KubeConfig
	}
	return rules
}

// overridesFrom maps the global kubectl flags onto client config overrides,
// as cli-runtime's ConfigFlags does. --cluster, --user, --server and
// --tls-server-name are absent: ValidateFleetFlags rejects them.
func overridesFrom(f *genericclioptions.ConfigFlags) *clientcmd.ConfigOverrides {
	o := &clientcmd.ConfigOverrides{ClusterDefaults: clientcmd.ClusterDefaults}
	if f == nil {
		return o
	}
	set := func(dst *string, src *string) {
		if src != nil {
			*dst = *src
		}
	}
	set(&o.AuthInfo.ClientCertificate, f.CertFile)
	set(&o.AuthInfo.ClientKey, f.KeyFile)
	set(&o.AuthInfo.Token, f.BearerToken)
	set(&o.AuthInfo.Impersonate, f.Impersonate)
	set(&o.AuthInfo.ImpersonateUID, f.ImpersonateUID)
	set(&o.AuthInfo.Username, f.Username)
	set(&o.AuthInfo.Password, f.Password)
	set(&o.ClusterInfo.CertificateAuthority, f.CAFile)
	set(&o.Context.Namespace, f.Namespace)
	set(&o.Timeout, f.Timeout)
	if f.ImpersonateGroup != nil {
		o.AuthInfo.ImpersonateGroups = *f.ImpersonateGroup
	}
	if f.Insecure != nil {
		o.ClusterInfo.InsecureSkipTLSVerify = *f.Insecure
	}
	if f.DisableCompression != nil {
		o.ClusterInfo.DisableCompression = *f.DisableCompression
	}
	return o
}

// defaultCacheDir mirrors kubectl: $KUBECACHEDIR, else ~/.kube/cache.
func defaultCacheDir() string {
	if d := os.Getenv("KUBECACHEDIR"); d != "" {
		return d
	}
	return filepath.Join(homedir.HomeDir(), ".kube", "cache")
}

var unsafeCacheChars = regexp.MustCompile(`[^(\w/.)]`)

// discoveryCacheDir mirrors kubectl's per-host directory naming so the two
// share one discovery cache.
func discoveryCacheDir(parent, host string) string {
	h := strings.Replace(strings.Replace(host, "https://", "", 1), "http://", "", 1)
	return filepath.Join(parent, unsafeCacheChars.ReplaceAllString(h, "_"))
}

// serverWarnings prints API server warnings (deprecated APIs and the like)
// to stderr as "warn: <context>: <message>", each once per run. client-go's
// default handler logs through klog, which main discards.
var serverWarnings = &warningPrinter{out: os.Stderr, seen: map[string]bool{}}

type warningPrinter struct {
	mu   sync.Mutex
	out  io.Writer
	seen map[string]bool
}

func (w *warningPrinter) forContext(ctxName string) rest.WarningHandler {
	return warningFunc(func(code int, _ string, message string) {
		if code != 299 || message == "" {
			return
		}
		key := ctxName + "\x00" + message
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.seen[key] {
			return
		}
		w.seen[key] = true
		_, _ = fmt.Fprintf(w.out, "warn: %s: %s\n", ctxName, message)
	})
}

type warningFunc func(code int, agent, message string)

func (f warningFunc) HandleWarningHeader(code int, agent, message string) { f(code, agent, message) }

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
	// A bearer token or password is a secret for one cluster. Sent across the
	// fleet it would reach every API server, so it needs --context.
	if kubeFlags.Context == nil || *kubeFlags.Context == "" {
		for _, s := range []struct {
			flag string
			val  *string
		}{{"--token", kubeFlags.BearerToken}, {"--username", kubeFlags.Username}, {"--password", kubeFlags.Password}} {
			if s.val != nil && *s.val != "" {
				return fmt.Errorf("%s requires --context: it would send the credential to every cluster in the fleet", s.flag)
			}
		}
	}
	return nil
}
