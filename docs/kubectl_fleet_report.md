## kubectl fleet report

Shareable fleet report: overview plus what needs attention

### Synopsis

Write one shareable report on the selected clusters: a verdict, the
items that need attention, a fleet overview and a row per cluster.

The report collects, per cluster, the control-plane and kubelet versions,
nodes ready/total, zones, instance types, how nodes are provisioned
(Karpenter NodePool, EKS node group or other), pods running, pending and
crashlooping, and the kube-system component versions. That is one node
list and one pod list per cluster.

Needs attention lists, most severe first: contexts that failed (including
those --where reachable skipped), clusters whose nodes or pods could not be
listed, NotReady nodes, crashlooping pods, pods Pending longer than
--pending-for, and, with --expect-version, control planes or kubelets on
another version. Partial kube-system data is listed as information only.

Formats: text (default; paste into a chat message as a code block),
markdown, html (one self-contained page), json and yaml (for scripts).
Failures appear as a short cause such as "timed out"; server addresses and
raw errors never reach the report. Context, namespace, node pool and node
group names, zones and instance types do.

Exit codes: 0 all clear, 2 the report lists something that needs attention,
1 no report was written (bad flags, unreadable kubeconfig, no contexts).

```
kubectl fleet report [flags]
```

### Examples

```
  # Are the production clusters healthy?
  kubectl fleet report --contexts '^prod-eu' --contexts '^prod-us'

  # Is every cluster on 1.30? Share it as a web page
  kubectl fleet report --expect-version 1.30 -o html > fleet.html

  # Markdown for a ticket or a wiki page
  kubectl fleet report --contexts '^prod-' -o markdown > fleet.md

  # In a script: alert only when something needs attention
  kubectl fleet report -o json > fleet.json || [ $? -eq 2 ] && notify fleet.json
```

### Options

```
      --contexts stringArray    regex selecting kubeconfig contexts by name; repeatable, any may match (default: all)
      --errors                  show each failed context's full error on stderr; the report itself only names the cause
      --exclude string          regex of context names to leave out, applied after --contexts
      --expect-version string   flag control planes and kubelets not on this version (1.30 matches any 1.30.x)
  -h, --help                    help for report
  -o, --output string           output format: text|markdown|html|json|yaml (default "text")
      --parallelism int         max contexts queried at once (0=unbounded) (default 32)
      --pending-for duration    a pod Pending at least this long counts as stuck (default 5m0s)
      --strict                  accepted for consistency; report already exits 2 when any context failed
      --where stringArray       filter contexts by live attributes; repeatable, all must match: version<1.30, nodes>=50, reachable, !reachable
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

