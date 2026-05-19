# Contributing to kubectl-fleet

Thanks for considering a contribution. This document covers local development,
testing, and the conventions enforced on every PR.

## Development environment

Required tooling:

- **Go** — version pinned in `go.mod` (≥ 1.22)
- **[mise](https://mise.jdx.dev/)** — runtime manager (`brew install mise` on macOS)
- **[just](https://just.systems/)** — task runner (installed by mise)
- **[golangci-lint](https://golangci-lint.run/)** — installed by mise
- **kubectl** — 1.12+ (for plugin discovery testing)
- A reachable Kubernetes cluster for end-to-end testing (k3d, kind, minikube)

Bootstrap:

```bash
git clone https://github.com/ethan-kane-ops/kubectl-fleet
cd kubectl-fleet
mise install      # installs pinned Go + linters
just check        # tidy + lint + test — must pass before any commit
```

## Common tasks

```bash
just                  # list all targets
just build            # compile to ./bin/kubectl-fleet
just run -- --help    # build + invoke via kubectl plugin protocol
just test             # go test ./...
just test-race        # race detector
just lint             # go vet + golangci-lint
just check            # tidy + lint + test (gating recipe)
just install          # go install ./... (binary on PATH for manual testing)
just release-snapshot # local goreleaser dry-run (no publish)
```

`just check` is the gate. Every commit and every PR must leave the tree green.

## Project layout

```
cmd/kubectl-fleet/main.go    Entry; ldflag-injected version/commit/date
internal/cmd/                Cobra subcommands; one verb per file
internal/kubeconfig/         Multi-context loader + REST config builder
internal/fleet/              Bounded parallel executor (errgroup with SetLimit)
internal/k8s/                Typed + dynamic + discovery client factory; GVR resolver
internal/output/             Table/JSON/YAML printers
internal/health/             Per-cluster summary used by `status`
```

New subcommands live in `internal/cmd/`, register in `NewRootCmd` (`root.go`), and
accept `*genericclioptions.ConfigFlags` so kubectl global flags (`--context`,
`--namespace`, `--kubeconfig`) work uniformly.

## Coding conventions

- **Error strings**: lowercase, no trailing punctuation, wrapped with `%w`.
  Example: `fmt.Errorf("resolve gvr: %w", err)` — not `"Failed to resolve GVR."`
- **Errors are aggregated, never fatal across the fleet.** A single cluster
  failing must not abort processing for the others. Use `internal/fleet.Run`
  and surface per-cluster errors in the result table.
- **Multi-cluster reads are parallel by default.** Bound via `--parallelism N`
  (default 8). Never block on a single slow cluster.
- **Tests**: table-driven; `t.TempDir()` for filesystem fixtures; fake clients
  from `k8s.io/client-go/kubernetes/fake` and `dynamic/fake`. Race detector
  must stay clean (`go test -race ./...`).
- **No backwards-compat shims** for removed code. Delete it.

## Commit messages

Conventional Commits format, enforced by review:

```
type(scope): short imperative summary
```

- `type` ∈ `feat`, `fix`, `chore`, `refactor`, `docs`, `test`, `style`, `ci`, `perf`
- `scope` = affected package or area (e.g., `cmd`, `fleet`, `argo`, `release`)
- Summary ≤ 50 characters, imperative mood, no trailing period
- Body (optional) explains *why*, not *what*; the diff covers *what*

Examples:

- `feat(cmd): add version subcommand with skew detection`
- `fix(fleet): propagate context cancellation through parallel workers`
- `refactor(output): extract table builder from printer`

## Pull request process

1. Branch from `main`: `git checkout -b feat/short-description` or
   `polish/v0.X.Y` for grouped polish work.
2. Keep PRs focused; one logical change per PR is ideal.
3. Run `just check` locally before pushing.
4. Open the PR. CI runs vet + race tests on every push.
5. Squash-merge is preferred; rebase-merge is acceptable. Merge commits are
   avoided so the changelog stays linear.

The `main` branch is protected: direct pushes are blocked, all changes land
through PRs. Tags are cut from `main` and only by maintainers (see *Releasing*).

## Releasing (maintainers only)

```bash
just release-snapshot    # local goreleaser dry-run — verify multi-arch builds
just release 0.1.1       # creates tag v0.1.1, pushes, fires release workflow
```

Goreleaser runs in GitHub Actions on tag push, produces multi-arch archives
(linux/darwin/windows × amd64/arm64) plus a `checksums.txt`, and attaches them
to the corresponding GitHub Release. Release notes are auto-generated from
commits between tags, grouped by Conventional-Commit type.

Versioning follows SemVer:

- `v0.x.y` — pre-1.0, breaking changes allowed in minor bumps
- Patch bumps for fixes and non-breaking polish between phases
- Minor bumps for new phases (Argo support, drift detection, etc.)
- `v1.0.0` will mark feature-complete and trigger krew submission

## Reporting issues

Open a GitHub issue with:

- `kubectl fleet version` output
- `kubectl version` output
- Minimal reproduction (kubeconfig snippet, command line, observed vs expected)
- For multi-cluster bugs: number of contexts and any regex used

Security issues: do **not** open a public issue. Email the maintainer or use
GitHub's private vulnerability reporting.
