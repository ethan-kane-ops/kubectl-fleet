# kubectl-fleet

Multi-cluster operational awareness for Kubernetes fleets.

`kubectl-fleet` is a kubectl plugin that fans queries out across every
kubeconfig context in parallel and rolls the results back into a single,
context-aware table. Use it to triage incidents, check rollout parity, or audit
fleet-wide state without juggling N terminal tabs.

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
VERSION="0.1.0"

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

## Commands

### `kubectl fleet contexts`

List kubeconfig contexts with an optional parallel reachability probe.

```bash
# raw list — no probe
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

```bash
# pods across the whole fleet, every namespace
kubectl fleet get pods -A

# deployments in one namespace, prod clusters only
kubectl fleet get deploy -n payments --contexts '^prod-'

# label selector
kubectl fleet get pods -A -l app=ingress-nginx --contexts '^prod-'

# single named resource — compare the same Deployment across clusters
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
`<error>` in the `NAME` column and surface a `warn:` line on stderr.

### `kubectl fleet status`

Per-cluster health snapshot: node readiness, pod counts, server version, and
the three noisiest namespaces by non-Running pods.

```bash
kubectl fleet status
kubectl fleet status --contexts '^prod-' -o wide
kubectl fleet status -o json | jq '.[] | select(.PodsCrashLoop > 0)'
```

Wide output adds `FAILED`, `TOTAL_PODS`, `TOP_NOISY`, and `ERROR` columns.

## Output formats

Every command honours `-o`:

| Format    | Description                                            |
| --------- | ------------------------------------------------------ |
| `table`   | Default. Human-readable, fixed column widths.          |
| `wide`    | `table` + additional diagnostic columns where useful.  |
| `json`    | Machine-readable, one document per row.                |
| `yaml`    | Same data as `json`, YAML-encoded.                     |

Pipe `-o json` into `jq` or `-o yaml` into `yq` for further filtering and
scripting.

## Flags inherited from kubectl

`kubectl-fleet` plumbs `genericclioptions.ConfigFlags`, so the standard
kubeconfig flags work uniformly:

- `--kubeconfig <path>` — alternate kubeconfig file
- `--context <name>` — restrict to a single context (overrides `--contexts`)
- `--namespace <ns>` / `-n <ns>` — namespace scope for `get`
- `--user`, `--cluster`, `--token`, `--server`, etc.

`KUBECONFIG` env var is honoured (colon-separated multi-file lists are merged).

## Requirements

- Go **1.22+** — only when installing via `go install` or building from source
- `kubectl` **1.12+** — for plugin discovery protocol
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
