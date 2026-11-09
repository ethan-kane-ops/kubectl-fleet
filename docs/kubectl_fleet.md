## kubectl fleet

Multi-cluster operational awareness for K8s fleets

### Synopsis

Run read-only kubectl operations across many clusters at once.

kubectl fleet queries every context in your kubeconfig in parallel and
merges the answers into one table, with a CONTEXT column. A cluster that
fails never stops the run: failures are summarised on stderr, grouped by
cause, and --strict turns them into a non-zero exit. When every context
fails the command exits non-zero regardless.

Choosing clusters (on every command):
  --contexts REGEX     contexts whose name matches (default: all)
  --exclude REGEX      leave out matching names, applied after --contexts
  --context NAME       exactly one context, overriding both
  --where EXPR         filter by live attributes, repeatable, all must match:
                       version<1.30, version>=1.29.3, nodes>50, reachable,
                       !reachable (contexts only)

Output and failures:
  -o table|wide|json|yaml|name, --no-headers
  --errors             show each failed context and its full error
  --strict             exit non-zero if any context failed
  --parallelism N      contexts queried at once (32; 16 for get)

kubectl's global flags (--kubeconfig, --as, --request-timeout, ...) apply
to every context. --cluster, --user, --server and --tls-server-name are
rejected, and --token needs --context, since across a fleet they would
point every context at one target or send one cluster's secret to all.

### Examples

```
  # Which contexts can I reach?
  kubectl fleet contexts --check

  # Health of every production cluster, biggest first
  kubectl fleet status --contexts '^prod-' --sort-by nodes

  # Crashlooping or unready pods anywhere in the fleet
  kubectl fleet get pods -A --problems

  # Control planes, kubelets and add-ons still on 1.29
  kubectl fleet version --detailed --where 'version<1.30'
```

### Options

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
  -h, --help                           help for kubectl fleet
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

* [kubectl fleet contexts](kubectl_fleet_contexts.md)	 - List kubeconfig contexts, with optional reachability probe
* [kubectl fleet get](kubectl_fleet_get.md)	 - Get a resource across all matching kubeconfig contexts
* [kubectl fleet status](kubectl_fleet_status.md)	 - Per-cluster health summary across kubeconfig contexts
* [kubectl fleet version](kubectl_fleet_version.md)	 - Print plugin and per-cluster Kubernetes server versions

