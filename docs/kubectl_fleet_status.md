## kubectl fleet status

Per-cluster health summary across kubeconfig contexts

### Synopsis

Summarise each cluster's health: control-plane version, nodes
ready/total, and pod counts (running, pending, crashlooping), with failed
pods, total pods and the noisiest namespaces under -o wide.

--problems keeps clusters with NotReady nodes or Pending, Failed or
crashlooping pods. --sort-by nodes|pods|version puts the largest (or
newest) first, --sort-by context orders by name, and --top N keeps the
first N after sorting. --since adds a count of containers restarted in
that window.

-o json|yaml prints one record per context with numeric fields; a failed
context has only context and error.

```
kubectl fleet status [flags]
```

### Examples

```
  # Every cluster
  kubectl fleet status

  # Only clusters with something wrong
  kubectl fleet status --problems

  # The 10 largest clusters by pod count
  kubectl fleet status --sort-by pods --top 10

  # Restarts in the last hour, production only
  kubectl fleet status --contexts '^prod-' --since 1h

  # Crashlooping clusters from JSON
  kubectl fleet status -o json | jq -r '.[] | select(.podsCrashLoop > 0) | .context'
```

### Options

```
      --contexts string     regex selecting kubeconfig contexts by name (default: all)
      --errors              show each failed context: an <error> row with its ERROR text, and one warning line per failure (default: a one-line summary)
      --exclude string      regex of context names to leave out, applied after --contexts
  -h, --help                help for status
      --no-headers          suppress header row in table/wide output
  -o, --output string       output format: table|wide|json|yaml|name (default "table")
      --parallelism int     max contexts queried at once (0=unbounded) (default 32)
      --problems            show only clusters with NotReady nodes or Pending, Failed or crashlooping pods
      --since duration      if set, add RESTARTS_<dur> column counting containers with LastTermination.FinishedAt within window (e.g. 5m, 1h)
      --sort-by string      order clusters: nodes (total), pods (running) or version, largest first; or context, by name
      --strict              exit non-zero if any context failed
      --top int             keep only the first N clusters after sorting (0 = all)
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

