## kubectl fleet version

Print plugin and per-cluster Kubernetes server versions

### Synopsis

Print the plugin's version and each cluster's control-plane version.

--detailed adds what runs on the nodes and in kube-system: kubelet, OS
image, kernel, container runtime and architecture (with node counts), and
the image tag of every kube-system Deployment and DaemonSet (CoreDNS,
kube-proxy, the CNI, CSI drivers...). By default it prints the fleet-wide
spread: each component's versions and how many clusters run each.
--per-cluster (or -o wide) gives one row per context, and -o json the full
per-cluster detail.

A cluster where you may not list nodes or kube-system workloads returns a
partial result, noted on stderr, rather than failing.

```
kubectl fleet version [flags]
```

### Examples

```
  # Control-plane version of every cluster
  kubectl fleet version

  # Version spread across the fleet: control planes, kubelets, add-ons
  kubectl fleet version --detailed

  # Kubelet skew and add-on versions per cluster, before an upgrade
  kubectl fleet version --detailed --per-cluster --where 'version<1.30'

  # Only the plugin's own version
  kubectl fleet version --client
```

### Options

```
      --client              show client version only, skip cluster probes
      --contexts string     regex selecting kubeconfig contexts by name (default: all)
      --detailed            also report kubelet, node OS, kernel, container runtime, architecture and kube-system component versions; by default as a fleet-wide spread
      --errors              show each failed context: an <error> row with its ERROR text, and one warning line per failure (default: a one-line summary)
      --exclude string      regex of context names to leave out, applied after --contexts
  -h, --help                help for version
      --no-headers          suppress header row in table/wide output
  -o, --output string       output format: table|wide|json|yaml|name (default "table")
      --parallelism int     max contexts queried at once (0=unbounded) (default 32)
      --per-cluster         with --detailed, one row per context and component instead of the fleet-wide spread
      --strict              exit non-zero if any context failed (ignored with --client)
      --where stringArray   filter contexts by live attributes; repeatable, all must match: version<1.30, nodes>=50, reachable, !reachable
```

### Options inherited from parent commands

```
      --as string                      Username to impersonate for the operation. User could be a regular user or a service account in a namespace.
      --as-group stringArray           Group to impersonate for the operation, this flag can be repeated to specify multiple groups.
      --as-uid string                  UID to impersonate for the operation.
      --cache-dir string               Default cache directory (default "$HOME/.kube/cache")
      --certificate-authority string   Path to a cert file for the certificate authority
      --client-certificate string      Path to a client certificate file for TLS
      --client-key string              Path to a client key file for TLS
      --cluster string                 The name of the kubeconfig cluster to use
      --context string                 The name of the kubeconfig context to use
      --disable-compression            If true, opt-out of response compression for all requests to the server
      --insecure-skip-tls-verify       If true, the server's certificate will not be checked for validity. This will make your HTTPS connections insecure
      --kubeconfig string              Path to the kubeconfig file to use for CLI requests.
  -n, --namespace string               If present, the namespace scope for this CLI request
      --request-timeout string         The length of time to wait before giving up on a single server request. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h). A value of zero means don't timeout requests. (default "0")
  -s, --server string                  The address and port of the Kubernetes API server
      --tls-server-name string         Server name to use for server certificate validation. If it is not provided, the hostname used to contact the server is used
      --token string                   Bearer token for authentication to the API server
      --user string                    The name of the kubeconfig user to use
```

### SEE ALSO

* [kubectl fleet](kubectl_fleet.md)	 - Multi-cluster operational awareness for K8s fleets

