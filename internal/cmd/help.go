package cmd

// Help text for each command. Examples cover a couple of clusters and a
// fleet of hundreds; flags shared by every fleet command are described once
// in rootLong.

const rootLong = `Run read-only kubectl operations across many clusters at once.

kubectl fleet queries every context in your kubeconfig in parallel and
merges the answers into one table, with a CONTEXT column. A cluster that
fails never stops the run: failures are summarised on stderr, grouped by
cause, and --strict turns them into a non-zero exit.

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
point every context at one target or send one cluster's secret to all.`

const rootExample = `  # Which contexts can I reach?
  kubectl fleet contexts --check

  # Health of every production cluster, biggest first
  kubectl fleet status --contexts '^prod-' --sort-by nodes

  # Crashlooping or unready pods anywhere in the fleet
  kubectl fleet get pods -A --problems

  # Control planes, kubelets and add-ons still on 1.29
  kubectl fleet version --detailed --where 'version<1.30'`

const contextsLong = `List kubeconfig contexts, optionally probing each one's API server.

Without --check this only reads your kubeconfig. --check calls /version on
every context (each with its own --timeout) and adds REACHABLE, VERSION,
LATENCY and ERROR columns. Unlike the other commands, contexts keeps a row
for every context it could not reach, since reachability is its subject.

--where works here too, and only here accepts '!reachable', to list the
contexts that are down along with why.`

const contextsExample = `  # Every context in the kubeconfig
  kubectl fleet contexts

  # Probe reachability, 2 seconds per context
  kubectl fleet contexts --check --timeout 2s

  # Just the ones that are down, with the cause
  kubectl fleet contexts --where '!reachable'

  # CI gate: fail if any production context is unreachable
  kubectl fleet contexts --contexts '^prod-' --check --strict`

const getLong = `Get a resource across every selected context, like kubectl get.

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
kubectl-fleet/context; it streams as clusters answer.`

const getExample = `  # Pods in each context's own namespace
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
  kubectl fleet get pods -A -l app=api -o name`

const statusLong = `Summarise each cluster's health: control-plane version, nodes
ready/total, and pod counts (running, pending, crashlooping), with failed
pods, total pods and the noisiest namespaces under -o wide.

--problems keeps clusters with NotReady nodes or Pending, Failed or
crashlooping pods. --sort-by nodes|pods|version puts the largest (or
newest) first, --sort-by context orders by name, and --top N keeps the
first N after sorting. --since adds a count of containers restarted in
that window.

-o json|yaml prints one record per context with numeric fields; a failed
context has only context and error.`

const statusExample = `  # Every cluster
  kubectl fleet status

  # Only clusters with something wrong
  kubectl fleet status --problems

  # The 10 largest clusters by pod count
  kubectl fleet status --sort-by pods --top 10

  # Restarts in the last hour, production only
  kubectl fleet status --contexts '^prod-' --since 1h

  # Crashlooping clusters from JSON
  kubectl fleet status -o json | jq -r '.[] | select(.podsCrashLoop > 0) | .context'`

const versionLong = `Print the plugin's version and each cluster's control-plane version.

--detailed adds what runs on the nodes and in kube-system: kubelet, OS
image, kernel, container runtime and architecture (with node counts), and
the image tag of every kube-system Deployment and DaemonSet (CoreDNS,
kube-proxy, the CNI, CSI drivers...). By default it prints the fleet-wide
spread: each component's versions and how many clusters run each.
--per-cluster (or -o wide) gives one row per context, and -o json the full
per-cluster detail.

A cluster where you may not list nodes or kube-system workloads returns a
partial result, noted on stderr, rather than failing.`

const versionExample = `  # Control-plane version of every cluster
  kubectl fleet version

  # Version spread across the fleet: control planes, kubelets, add-ons
  kubectl fleet version --detailed

  # Kubelet skew and add-on versions per cluster, before an upgrade
  kubectl fleet version --detailed --per-cluster --where 'version<1.30'

  # Only the plugin's own version
  kubectl fleet version --client`
