## kubectl fleet get

Get a resource across all matching kubeconfig contexts

### Synopsis

Get a resource across every selected context, like kubectl get.

Columns come from each API server, exactly as kubectl shows them,
including a custom resource's own printer columns. Clusters on different
versions can render different columns; they are merged by name and a
cluster leaves blank what it lacks. --server-print=false uses the plugin's
built-in columns, which are also the fallback for an API that cannot
render tables.

Without -n or -A each context is queried in its own namespace (default
"default"). get KIND NAME skips contexts that don't have the object and
fails only when none do; with -A it finds the name in every namespace.

--problems keeps only unhealthy rows, judged from the STATUS and READY
columns of any kind: a STATUS other than Running, Completed, Ready, Bound
and similar, or READY short of its target (1/2). Finished Job pods and
cordoned nodes count as healthy.

-o json|yaml prints a v1 List of full objects, each annotated with
kubectl-fleet/context; it streams as clusters answer.

```
kubectl fleet get KIND [NAME] [flags]
```

### Examples

```
  # Pods in each context's own namespace
  kubectl fleet get pods

  # Deployments in one namespace on production clusters
  kubectl fleet get deploy -n payments --contexts '^prod-'

  # Only unhealthy pods, fleet-wide
  kubectl fleet get pods -A --problems

  # Where does the api deployment run, in any namespace?
  kubectl fleet get deploy api -A

  # Argo CD applications with their sync and health columns
  kubectl fleet get applications -A

  # Scripting: one context/namespace/name per line
  kubectl fleet get pods -A -l app=api -o name
```

### Options

```
  -A, --all-namespaces      query across all namespaces
      --contexts string     regex selecting kubeconfig contexts by name (default: all)
      --errors              show each failed context: an <error> row with its ERROR text, and one warning line per failure (default: a one-line summary)
      --exclude string      regex of context names to leave out, applied after --contexts
  -h, --help                help for get
      --no-headers          suppress header row in table/wide output
  -o, --output string       output format: table|wide|json|yaml|name (default "table")
      --parallelism int     max contexts queried at once (0=unbounded); each in-flight context holds a page of up to 500 decoded objects (default 16)
      --problems            show only unhealthy rows: STATUS not healthy (Running, Completed, Ready, Bound…) or READY short (1/2)
  -l, --selector string     label selector
      --server-print        let each API server render the table columns, as kubectl does; false uses built-in columns (default true)
      --strict              exit non-zero if any context failed
      --where stringArray   filter contexts by live attributes; repeatable, all must match: version<1.30, nodes>=50, reachable, !reachable
```

### Options inherited from parent commands

```
      --as string                      Username to impersonate for the operation. User could be a regular user or a service account in a namespace.
      --as-group stringArray           Group to impersonate for the operation, this flag can be repeated to specify multiple groups.
      --as-uid string                  UID to impersonate for the operation.
      --as-user-extra stringArray      User extras to impersonate for the operation, this flag can be repeated to specify multiple values for the same key.
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
      --proxy-url string               Proxy URL to use for requests to the API server
      --request-timeout string         The length of time to wait before giving up on a single server request. Non-zero values should contain a corresponding time unit (e.g. 1s, 2m, 3h). A value of zero means don't timeout requests. (default "0")
  -s, --server string                  The address and port of the Kubernetes API server
      --tls-server-name string         Server name to use for server certificate validation. If it is not provided, the hostname used to contact the server is used
      --token string                   Bearer token for authentication to the API server
      --user string                    The name of the kubeconfig user to use
```

### SEE ALSO

* [kubectl fleet](kubectl_fleet.md)	 - Multi-cluster operational awareness for K8s fleets

