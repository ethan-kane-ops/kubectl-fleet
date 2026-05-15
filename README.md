# kubectl-fleet

Multi-cluster operational awareness for K8s fleets.

A kubectl plugin. Drop the `kubectl-fleet` binary anywhere on `PATH` and
`kubectl fleet` works via kubectl's plugin discovery protocol.

## Install

From a tagged release (any platform):

```bash
# pick the right asset for your OS/arch
VERSION=0.1.0
curl -L -o kubectl-fleet.tar.gz \
  "https://github.com/ethan-kane-ops/kubectl-fleet/releases/download/v${VERSION}/kubectl-fleet_v${VERSION}_$(uname -s | tr A-Z a-z)_$(uname -m).tar.gz"
tar -xzf kubectl-fleet.tar.gz
sudo mv kubectl-fleet /usr/local/bin/
kubectl fleet --help
```

With Go installed:

```bash
go install github.com/ethan-kane-ops/kubectl-fleet/cmd/kubectl-fleet@latest
kubectl fleet --help
```

From source:

```bash
git clone https://github.com/ethan-kane-ops/kubectl-fleet
cd kubectl-fleet
just install
kubectl fleet --help
```

> **krew distribution:** deferred until the plugin is feature-complete. Once
> stable, a manifest will be submitted to
> [`kubernetes-sigs/krew-index`](https://github.com/kubernetes-sigs/krew-index)
> so `kubectl krew install fleet` becomes the recommended path.

## Requirements

- Go 1.22+
- [mise](https://mise.jdx.dev/) — runtime manager (`brew install mise`)
- [just](https://just.systems/) — task runner (managed by mise)
- `kubectl` 1.12+ (any version with the plugin discovery protocol)

## Development

```bash
mise install        # install pinned Go + tools
just check          # tidy + lint + test
just run -- --help  # build + invoke via kubectl plugin protocol
```

## Releasing

```bash
just release 0.1.0  # tag v0.1.0 → push → GH Actions runs goreleaser
```

Goreleaser produces multi-arch archives + checksums attached to the GitHub
Release. Users install per the **Install** section above.
