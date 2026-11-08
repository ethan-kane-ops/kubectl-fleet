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

> **krew distribution:** deferred until the plugin is feature-complete. Once
> stable, a manifest will be submitted to
> [`kubernetes-sigs/krew-index`](https://github.com/kubernetes-sigs/krew-index)
> so `kubectl krew install fleet` becomes the recommended path.

### Shell completion

`kubectl-fleet` gets Cobra's completion generator for free. It's scoped to
the `kubectl-fleet` binary name (not `kubectl`), so it completes direct
invocations without touching real kubectl's own completion:

```bash
# bash (current shell only)
source <(kubectl-fleet completion bash)

# zsh, written to your fpath
kubectl-fleet completion zsh > "${fpath[1]}/_kubectl-fleet"

# fish
kubectl-fleet completion fish | source
```

Run `kubectl-fleet completion --help` for persistent per-shell install
instructions. This completes `kubectl-fleet <TAB>`, not `kubectl fleet <TAB>`,
since kubectl doesn't forward completion requests to plugins.

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
kubectl fleet contexts --filter '^prod-' --check
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
```

Sample output:

```
CONTEXT      NAMESPACE    NAME                          AGE
prod-eu-1    payments     api-7d4f6c5b9-abcde           4d
prod-us-1    payments     api-7d4f6c5b9-fghij           4d
prod-ap-1    payments     api-9b2a4e1d3-klmno           1h
```

Failed clusters never abort the run. Per-context errors land in the table with
`<error>` in the `NAME` column (left out of `-o name|json|yaml`) and surface a `warn:`
line on stderr. Without `-n` or `-A`, each context is queried in its own
namespace, falling back to `default`. `get <kind> <name>` skips contexts that
don't have the object and errors only when none of them do. With `-A`, it
finds the name in every namespace of every context.

### `kubectl fleet status`

Per-cluster health snapshot: node readiness, pod counts, server version, and
the three noisiest namespaces by unhealthy pods (Pending, Failed, Unknown or
crashlooping; completed Job pods don't count).

```bash
kubectl fleet status
kubectl fleet status --contexts '^prod-' -o wide
kubectl fleet status -o json | jq '.[] | select(.podsCrashLoop > 0) | .context'
```

Wide output adds `FAILED`, `TOTAL_PODS`, `TOP_NOISY`, and `ERROR` columns.

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
reported on stderr instead. `-o name` joins fields with `/`, so it is ambiguous for
context names that contain one (EKS ARNs, for example); use `-o json` there.

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

Contexts run in parallel: `--parallelism` defaults to 32, or 16 for `get`,
which holds a page of decoded objects per in-flight context. If your
credential plugins prompt interactively (MFA codes, browser SSO), run with
`--parallelism 1` so the prompts don't interleave.

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
