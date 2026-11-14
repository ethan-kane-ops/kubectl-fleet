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
the values of `-o` and `--sort-by`. Kinds and names come from
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
kubectl fleet contexts --unreachable
```

Sample output:

```
CONTEXT     CLUSTER     NAMESPACE     AUTH        REACHABLE   VERSION   LATENCY   ERROR
prod-eu-1   prod-eu-1                 prod-eu-1   yes         v1.31.2   42ms
prod-us-1   prod-us-1                 prod-us-1   yes         v1.31.2   128ms
prod-ap-1   prod-ap-1                 prod-ap-1   yes         v1.30.6   310ms
dev-local   k3d-dev     kube-system   dev-admin   no                    3s        server version: Get "https://10.20.0.4:6443/version?timeout=3s": dial tcp 10.20.0.4:6443: i/o timeout
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
(never in `-o name|json|yaml`) and one `warn:` line each. When every
context fails, the command exits non-zero even without `--strict`. Without `-n` or `-A`, each context is queried in its own
namespace, falling back to `default`. `get KIND NAME` skips contexts that
don't have the object and errors only when none of them do. With `-A`, it
finds the name in every namespace of every context.

### `kubectl fleet status`

Per-cluster health snapshot: node readiness, pod counts, server version, and
the three namespaces with the most Pending or Failed pods (completed Job pods
don't count). Running pods are read as compact server-side table rows of a
few dozen bytes each, which carry no namespace, so crashlooping pods are
counted but not placed in a namespace; `--since` reads Running pods in full.

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

### `kubectl fleet report`

One shareable report for when someone asks "are the prod clusters healthy?"
or "is everything on 1.30 yet?": a verdict, what needs attention, a fleet
overview and a row per cluster.

```
$ kubectl fleet report --contexts '^prod-' --expect-version 1.30
FLEET REPORT  2026-11-14 10:30 UTC
Selection: contexts ^prod-; expecting version 1.30; pending counts after 5m
3 clusters (2 reachable)

3 ITEMS NEED ATTENTION
  [FAILED]     prod-ap-1  context failed: connection refused
  [CRASHLOOP]  prod-us-1  2 crashlooping pods (payments(2))
  [VERSION]    prod-us-1  kubelets not on 1.30: v1.29.8-eks-a737599(1)

FLEET OVERVIEW
  Clusters                   3 (2 reachable)
  Nodes                      7 of 7 ready
  Pods                       20 running, 0 pending (0 stuck), 2 crashlooping, 0 failed, 20 total
  Control planes (clusters)  v1.30.4-eks-a737599 (2)
  Kubelets (nodes)           v1.30.4-eks-a737599 (6), v1.29.8-eks-a737599 (1)
  Zones (nodes)              eu-west-1a (5), eu-west-1b (2)
  Instance types (nodes)     m7g.large (6), unknown (1)
  Provisioners (nodes)       karpenter/general (6), nodegroup/system (1)

CLUSTERS
  CONTEXT    VERSION  KUBELETS       NODES  ZONES  PODS  PENDING  CRASH  PROVISIONERS
  prod-ap-1  -        -              -      -      -     -        -      failed: connection refused
  prod-eu-1  1.30.4   1.30.4         3/3    1      10    0        0      karpenter 3
  prod-us-1  1.30.4   1.29.8-1.30.4  4/4    2      10    0        2      karpenter 3, nodegroup 1
```

```bash
kubectl fleet report --contexts '^prod-eu' --contexts '^prod-us'   # named clusters
kubectl fleet report --expect-version 1.30 -o html > fleet.html    # one self-contained page
kubectl fleet report -o markdown > fleet.md                        # for a ticket or wiki
kubectl fleet report -o json                                       # for scripts
```

Attention items, most severe first: contexts that failed (including any
`--skip-unreachable` skipped), clusters whose nodes or pods could not be
listed, NotReady nodes, crashlooping pods, pods Pending longer than
`--pending-for` (default `5m`), and control planes or kubelets off
`--expect-version` (`1.30` matches any `1.30.x`). Zones, instance types and
provisioners (Karpenter NodePool, EKS node group, other) come from standard
node labels, at no extra API cost.

The text form is plain ASCII, so it pastes into a chat message as a code
block. The HTML form needs no network and no JavaScript. Failures show as a
short cause such as `timed out`; server addresses and raw errors never reach
the report (`--errors` prints them on stderr). Context, namespace, node pool
and node group names do appear.

Exit codes: `0` all clear, `2` the report lists something that needs
attention, `1` no report was written. A scheduled job can keep the JSON and
alert only on `2`.

## Choosing clusters

Every command takes the same selectors:

| Flag | Selects |
| ---- | ------- |
| `--contexts REGEX` | contexts whose name matches; repeatable, any may match (default: all) |
| `--exclude REGEX` | leaves out matching names, applied after `--contexts` |
| `--context NAME` | exactly one context, overriding both |
| `--version V` | server version matches `V`; repeatable, any may match |
| `--skip-unreachable` | leaves out clusters that cannot be reached, as a skip rather than a failure (not on `contexts`, which has `--unreachable`) |

`--version` compares at the precision you type (`1.29` matches any
`1.29.x`, `1.29.8` only that patch) and ignores vendor suffixes such as
`-eks-…`; it is the same rule `report --expect-version` uses. Repeat it for
several versions: `--version 1.28 --version 1.29`. "Cannot be reached"
means a network failure (timeout, connection refused, DNS); a cluster that
answers 401 or 403 is reachable and still counts as failed. Both flags
cost one quick `/version` call per context, and only when used.
`contexts --unreachable` lists the clusters that are down, with the cause.

## Recipes

```bash
# Crashlooping or unready pods across production
kubectl fleet get pods -A --problems --contexts '^prod-'

# Which clusters are still on 1.29, and what runs on them
kubectl fleet version --detailed --per-cluster --version 1.29

# Kubelet skew and add-on versions across the fleet before an upgrade
kubectl fleet version --detailed

# The 10 biggest clusters by node count
kubectl fleet status --sort-by nodes --top 10

# Everything except dev and staging, skipping clusters that are down
kubectl fleet status --exclude '^(dev|stage)-' --skip-unreachable

# What failed, and why
kubectl fleet status --errors

# Where does a deployment run, in any namespace?
kubectl fleet get deploy api -A
```

## Large fleets

kubectl-fleet is load-tested at 200 contexts (20 of them unreachable) and
at 20 clusters of 10,000 pods each (`just loadtest`). An opt-in suite
mimics a large EKS fleet from a laptop: 300 clusters of 5,000 pods and 60
nodes, 80 ms per request, TLS, and one exec credential plugin per cluster
burning 400 ms of CPU like `aws eks get-token`
(`just loadtest -suite work`, add `-bandwidth 25` to cap the link at
25 MB/s). The fake servers gzip their lists like a real API server and
their pods carry the per-pod IDs that keep real lists from compressing well
(about 485 B per pod on the wire, against about 520 B measured on a
kubernetes v1.37 cluster). On an M-series laptop:

| Command | Uncapped | 25 MB/s link |
| ------- | -------- | ------------ |
| `contexts --check`, `version` | 5 s | 5 s |
| `version --detailed` | 7 s | 7 s |
| `status` | 14 s | 14 s |
| `report` | 16 s | 16 s |
| `report --parallelism 96` | 6 s | 6 s |

`status` and `report` read Running pods as compact table rows (under 20 B
each on the wire) and only Pending and Failed pods in full, so a slow link no
longer limits them: request latency does, and `--parallelism` helps. Reading
every pod in full took 32 s for `report` on the 25 MB/s link. Exec
credential plugins cost about 4 s per 300 clusters at the default
parallelism.

- **Parallelism.** `--parallelism` (default 32, 16 for `get`) caps how many
  contexts are queried at once. Each API server only ever sees its own
  requests, so this protects your machine, not the clusters. `get` holds a
  page of up to 500 decoded objects per in-flight context.
- **Timeouts.** A cluster that never answers holds its slot until the
  request times out. Set `--request-timeout 30s` on large fleets so one
  wedged cluster can't stretch the run.
- **Failures.** Failed contexts are summarised in one stderr line, grouped
  by cause. Add `--errors` for each context's full error and `--strict` to
  fail the run when any context fails (it always fails when all do).
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
| `markdown` | A Markdown table (`md` also works), for tickets and wikis. |
| `html`    | One standalone HTML page with the table, no scripts.  |

`json` and `yaml` shapes:

- `get`: a `v1 List` of the full objects, as `kubectl get -o json` prints.
  Each object carries a `kubectl-fleet/context` annotation naming its context.
- `status`: an array of per-context records (`context`, `serverVersion`,
  `nodesReady`, `podsCrashLoop`, `topNoisyNamespaces`, ...). A failed context
  has only `context` and `error`.
- `contexts`, `version`: one object per table row, keyed by column header.
- `report`: one document with `generatedAt`, `selection`, `summary`,
  `attention`, `clusters` and `components`. A failed cluster carries only
  `context` and `error` (the short cause).

`markdown` and `html` show the regular columns, not the wide ones
(`contexts --check` keeps its probe columns). `report` takes
`text|markdown|html|json|yaml`, with `text` as its default.

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
