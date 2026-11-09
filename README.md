# kubectl-fleet

[![CI](https://github.com/ethan-kane-ops/kubectl-fleet/actions/workflows/ci.yml/badge.svg)](https://github.com/ethan-kane-ops/kubectl-fleet/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/ethan-kane-ops/kubectl-fleet.svg)](https://pkg.go.dev/github.com/ethan-kane-ops/kubectl-fleet)
[![Latest release](https://img.shields.io/github/v/release/ethan-kane-ops/kubectl-fleet)](https://github.com/ethan-kane-ops/kubectl-fleet/releases)
[![License](https://img.shields.io/github/license/ethan-kane-ops/kubectl-fleet)](LICENSE)

Multi-cluster operational awareness for Kubernetes fleets.

`kubectl-fleet` is a kubectl plugin that fans queries out across every
kubeconfig context in parallel and rolls the results back into a single,
context-aware table. Use it to triage incidents, check rollout parity, or audit
fleet-wide state without juggling N terminal tabs.

![kubectl fleet status, get, and contexts --check across three clusters](demo.gif)

```
$ kubectl fleet status --contexts '^prod-'
CONTEXT      VERSION      NODES   PODS   PENDING   CRASHLOOP
prod-eu-1    v1.31.2      6/6     142    0         0
prod-us-1    v1.31.2      6/6     138    2         1
prod-ap-1    v1.30.6      4/4     97     0         0
```

## Install

### From a tagged GitHub Release

```bash
# detect OS + arch (handles macOS arm64 vs linux aarch64 → arm64)
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')"
# latest release tag without the leading "v"; set VERSION by hand to pin one
VERSION="$(curl -fsSL https://api.github.com/repos/ethan-kane-ops/kubectl-fleet/releases/latest \
  | sed -n 's/.*"tag_name": *"v\([^"]*\)".*/\1/p')"

curl -L -o /tmp/kubectl-fleet.tar.gz \
  "https://github.com/ethan-kane-ops/kubectl-fleet/releases/download/v${VERSION}/kubectl-fleet_v${VERSION}_${OS}_${ARCH}.tar.gz"
tar -xzf /tmp/kubectl-fleet.tar.gz -C /tmp
sudo mv /tmp/kubectl-fleet /usr/local/bin/
kubectl fleet --help
```

### With Go installed

```bash
go install github.com/ethan-kane-ops/kubectl-fleet/cmd/kubectl-fleet@latest
kubectl fleet --help
```

### From source

```bash
git clone https://github.com/ethan-kane-ops/kubectl-fleet
cd kubectl-fleet
just install      # go install → on PATH
kubectl fleet --help
```

> kubectl-fleet is not distributed through krew. Install it from a GitHub
> Release, with `go install`, or from source as above.

### Shell completion

Completion covers subcommands, flags, context names (`--context`,
`--contexts`, `--exclude`), resource kinds and object names for `get`, and
the values of `-o`, `--sort-by` and `--where`. Kinds and names come from
the clusters themselves, capped at 2.5 seconds: on a large or partly
unreachable fleet, completion offers what answered in time rather than
hanging the shell.

**`kubectl fleet <TAB>`** (kubectl 1.26+): put the `kubectl_complete-fleet`
helper on your PATH next to `kubectl-fleet`, with kubectl's own completion
enabled. Release archives include it; `just install` installs it.

```bash
install -m 0755 kubectl_complete-fleet "$(dirname "$(command -v kubectl-fleet)")"
```

**`kubectl-fleet <TAB>`** (calling the binary directly):

```bash
# bash (current shell only)
source <(kubectl-fleet completion bash)

# zsh, written to your fpath
kubectl-fleet completion zsh > "${fpath[1]}/_kubectl-fleet"

# fish
kubectl-fleet completion fish | source
```

Run `kubectl-fleet completion --help` for persistent per-shell install
instructions.

## Commands

Full generated reference for every subcommand and flag lives in
[`docs/`](docs/kubectl_fleet.md).

### `kubectl fleet contexts`

List kubeconfig contexts with an optional parallel reachability probe.

```bash
# raw list, no probe
kubectl fleet contexts

# probe every context's /version endpoint (parallel)
kubectl fleet contexts --check --timeout 3s

# narrow to a subset via regex on context name
kubectl fleet contexts --contexts '^prod-' --check

# only the contexts that are down, with the cause
kubectl fleet contexts --where '!reachable'
```

Sample output:

```
CONTEXT      CLUSTER       NAMESPACE     REACHABLE   VERSION     LATENCY
prod-eu-1    prod-eu-1     default       yes         v1.31.2     42ms
prod-us-1    prod-us-1     default       yes         v1.31.2     128ms
prod-ap-1    prod-ap-1     default       yes         v1.30.6     310ms
dev-local    k3d-dev       kube-system   no          -           -
```

### `kubectl fleet get`

Parallel `kubectl get` across selected contexts. Adds a leading `CONTEXT`
column so identically-named resources across clusters are easy to compare.

Columns are rendered by each API server, exactly as `kubectl get` shows them,
including a CRD's own printer columns (an Argo CD `Application` shows its sync
and health status). When clusters run different versions and render
different columns, they are merged by name and a cluster leaves blank what
it lacks. `--server-print=false` uses the plugin's built-in columns instead,
which is also the automatic fallback for an API that cannot render tables.

```bash
# pods across the whole fleet, every namespace
kubectl fleet get pods -A

# deployments in one namespace, prod clusters only
kubectl fleet get deploy -n payments --contexts '^prod-'

# label selector
kubectl fleet get pods -A -l app=ingress-nginx --contexts '^prod-'

# single named resource: compare the same Deployment across clusters
kubectl fleet get deploy api -n payments --contexts '^prod-'

# only unhealthy pods, fleet-wide
kubectl fleet get pods -A --problems
```

Sample output:

```
CONTEXT      NAMESPACE    NAME                    READY   STATUS             RESTARTS   AGE
prod-eu-1    payments     api-7d4f6c5b9-abcde     1/1     Running            0          4d
prod-us-1    payments     api-7d4f6c5b9-fghij     0/1     CrashLoopBackOff   12         4d
prod-ap-1    payments     api-9b2a4e1d3-klmno     1/1     Running            0          1h
```

`--problems` keeps only unhealthy rows, judged from the STATUS and READY
columns of any kind: a STATUS other than Running, Completed, Ready, Bound
and similar, or READY short of its target (`1/2`). Finished Job pods and
cordoned nodes count as healthy.

Failed clusters never abort the run. A one-line summary on stderr counts them
and groups them by cause (`warn: 3 of 40 contexts failed: 2 timed out
(prod-eu-3, prod-eu-7), 1 forbidden (dev-a). Use --errors for details.`).
`--errors` adds an `<error>` row with an `ERROR` column per failed context
(never in `-o name|json|yaml`) and one `warn:` line each. Without `-n` or `-A`, each context is queried in its own
namespace, falling back to `default`. `get KIND NAME` skips contexts that
don't have the object and errors only when none of them do. With `-A`, it
finds the name in every namespace of every context.

### `kubectl fleet status`

Per-cluster health snapshot: node readiness, pod counts, server version, and
the three noisiest namespaces by unhealthy pods (Pending, Failed, Unknown or
crashlooping; completed Job pods don't count).

```bash
kubectl fleet status
kubectl fleet status --contexts '^prod-' -o wide
kubectl fleet status --problems                    # only clusters with something wrong
kubectl fleet status --sort-by pods --top 10       # the 10 biggest clusters by pods
kubectl fleet status -o json | jq '.[] | select(.podsCrashLoop > 0) | .context'
```

Wide output adds `FAILED`, `TOTAL_PODS` and `TOP_NOISY` columns.
`--sort-by nodes|pods|version` puts the largest (or newest) first and
`--sort-by context` orders by name.

### `kubectl fleet version`

The plugin's version and every cluster's control-plane version. With
`--detailed`, also what runs on the nodes and in kube-system: kubelet, OS
image, kernel, container runtime and architecture, plus the image tag of
every kube-system Deployment and DaemonSet (CoreDNS, kube-proxy, the CNI,
CSI drivers). By default that prints the fleet-wide spread:

```
$ kubectl fleet version --detailed
COMPONENT           VERSION                         CLUSTERS
kube-apiserver      v1.30.4-eks-a737599             180
kube-apiserver      v1.29.8-eks-a737599             20
kubelet             v1.30.4-eks-a737599             175
kubelet             v1.29.8-eks-a737599             25
os-image            Bottlerocket OS 1.20.0          200
container-runtime   containerd://1.7.13             200
coredns             v1.11.1-eksbuild.9              180
coredns             v1.10.1-eksbuild.13             20
```

`--per-cluster` (or `-o wide`) gives one row per context, with node counts
for kubelet and OS values; `-o json` carries everything per cluster. A
cluster where you can't list nodes or kube-system workloads returns a
partial result, noted on stderr, instead of failing.

## Choosing clusters

Every command takes the same selectors:

| Flag | Selects |
| ---- | ------- |
| `--contexts REGEX` | contexts whose name matches (default: all) |
| `--exclude REGEX` | leaves out matching names, applied after `--contexts` |
| `--context NAME` | exactly one context, overriding both |
| `--where EXPR` | live attributes, repeatable, all must match |

`--where` understands `version` (`version<1.30`, `version>=1.29.3`; compared
at the precision you type, vendor suffixes such as `-eks-…` ignored),
`nodes` (`nodes>50`) and `reachable` / `!reachable`. It costs one quick
probe per context, and only when used. `reachable` skips unreachable
contexts without counting them as failures; `!reachable` works only on
`contexts`, where it lists the dead ones with the cause.

## Recipes

```bash
# Crashlooping or unready pods across production
kubectl fleet get pods -A --problems --contexts '^prod-'

# Which clusters are still on 1.29, and what runs on them
kubectl fleet version --detailed --per-cluster --where 'version<1.30'

# Kubelet skew and add-on versions across the fleet before an upgrade
kubectl fleet version --detailed

# The 10 biggest clusters by node count
kubectl fleet status --sort-by nodes --top 10

# Everything except dev and staging, skipping clusters that are down
kubectl fleet status --exclude '^(dev|stage)-' --where reachable

# What failed, and why
kubectl fleet status --errors

# Where does a deployment run, in any namespace?
kubectl fleet get deploy api -A
```

## Large fleets

kubectl-fleet is load-tested at 300 contexts and at 20 clusters of 10,000
pods each (`just loadtest`).

- **Parallelism.** `--parallelism` (default 32, 16 for `get`) caps how many
  contexts are queried at once. Each API server only ever sees its own
  requests, so this protects your machine, not the clusters. `get` holds a
  page of up to 500 decoded objects per in-flight context.
- **Timeouts.** A cluster that never answers holds its slot until the
  request times out. Set `--request-timeout 30s` on large fleets so one
  wedged cluster can't stretch the run.
- **Failures.** Failed contexts are summarised in one stderr line, grouped
  by cause. Add `--errors` for each context's full error and `--strict` to
  fail the run.
- **Discovery cache.** Resource discovery is cached per cluster in
  `~/.kube/cache` (shared with kubectl), so repeated runs mostly skip it.
- **Interactive login plugins.** Credential plugins that prompt (MFA codes,
  browser SSO) run once per context and in parallel; use
  `--parallelism 1` so prompts don't interleave.
- **Memory.** Table output keeps rows, not objects; `-o json|yaml` streams
  as contexts finish.

## Output formats

Every command honours `-o`:

| Format    | Description                                            |
| --------- | ------------------------------------------------------ |
| `table`   | Default. Human-readable, fixed column widths.          |
| `wide`    | `table` + additional diagnostic columns where useful.  |
| `json`    | Machine-readable; shape per command, below.            |
| `yaml`    | Same data as `json`, YAML-encoded.                     |
| `name`    | `context/namespace/name` per line, for scripting.      |

`json` and `yaml` shapes:

- `get`: a `v1 List` of the full objects, as `kubectl get -o json` prints.
  Each object carries a `kubectl-fleet/context` annotation naming its context.
- `status`: an array of per-context records (`context`, `serverVersion`,
  `nodesReady`, `podsCrashLoop`, `topNoisyNamespaces`, ...). A failed context
  has only `context` and `error`.
- `contexts`, `version`: one object per table row, keyed by column header.

Failed contexts are left out of `get`'s `json`, `yaml` and `name` output and
reported on stderr instead. `-o name` joins fields with `/`, so it is
ambiguous for context names that contain one (EKS ARNs, for example); use
`-o json` there.

```bash
kubectl fleet get deploy -A -o json \
  | jq -r '.items[] | select(.status.readyReplicas != .spec.replicas)
           | "\(.metadata.annotations["kubectl-fleet/context"])/\(.metadata.name)"'
```

## Flags inherited from kubectl

`kubectl-fleet` plumbs `genericclioptions.ConfigFlags`, so the standard
kubeconfig flags apply to every context:

- `--kubeconfig <path>`: alternate kubeconfig file
- `--context <name>`: restrict to a single context (overrides `--contexts`)
- `--namespace <ns>` / `-n <ns>`: namespace scope for `get`
- `--as`, `--as-group`, `--request-timeout`, `--insecure-skip-tls-verify`,
  `--certificate-authority`, `--cache-dir`, etc.

`--cluster`, `--user`, `--server` and `--tls-server-name` are rejected: across
a fleet they would point every context at the same target. `--token` needs
`--context`, so one cluster's bearer token is never sent to the others.

API server warnings, such as deprecated API versions, print to stderr once
per context as `warn: <context>: <message>`.

`KUBECONFIG` env var is honoured (colon-separated multi-file lists are merged).

## Requirements

- Go at the version in `go.mod` (only when installing via `go install` or building from source)
- `kubectl` **1.12+** (for plugin discovery protocol)
- A reachable kubeconfig with one or more contexts

## Development

Contributions welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for local setup,
testing, and the conventional commit format used in this repository.

```bash
mise install        # install pinned Go + tools
just check          # tidy + lint + test (gating recipe)
just run -- --help  # build + invoke via kubectl plugin protocol
```

## Releasing (maintainers)

```bash
just release 0.1.1  # tag v0.1.1 → push → GH Actions runs goreleaser
```

Goreleaser produces multi-arch archives (`linux`, `darwin`, `windows` ×
`amd64`, `arm64`) plus `checksums.txt`, attaches them to the GitHub Release,
and renders auto-grouped release notes from Conventional-Commit messages.

## License

[Apache License 2.0](LICENSE).
