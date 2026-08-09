# Contributing to kubectl-fleet

Thanks for considering a contribution. This document covers local development,
testing, and the conventions enforced on every PR.

## Development environment

Required tooling:

- **Go**: version pinned in `go.mod` (≥ 1.22)
- **[mise](https://mise.jdx.dev/)**: runtime manager (`brew install mise` on macOS)
- **[just](https://just.systems/)**: task runner (installed by mise)
- **[golangci-lint](https://golangci-lint.run/)**: installed by mise
- **kubectl**: 1.12+ (for plugin discovery testing)
- **[kind](https://kind.sigs.k8s.io/)** + Docker (or Colima): for end-to-end testing (`just e2e`)

Bootstrap:

```bash
git clone https://github.com/ethan-kane-ops/kubectl-fleet
cd kubectl-fleet
mise install      # installs pinned Go + linters
just check        # tidy + lint + test, must pass before any commit
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
just docs             # regenerate command reference in ./docs
just release-snapshot # local goreleaser dry-run (no publish)
just e2e-up           # create 2 local kind clusters + apply fixtures
just e2e-test         # run e2e suite against clusters from e2e-up (repeatable)
just e2e-down         # delete the e2e kind clusters
just e2e              # full loop: up, test, always tear down after (what CI runs)
```

`docs/` is generated from the cobra command tree, not hand-written. Run
`just docs` after adding or changing a subcommand or flag, and commit the
result. CI fails the build if `docs/` is out of sync with the code
(`go run ./cmd/gendocs && git diff --exit-code docs`).

`just e2e` builds the real binary and runs it as a subprocess against two
real kind clusters (`fleet-e2e-a`/`fleet-e2e-b`), one seeded with a healthy
deployment and one with a crash-looping one, so `status`/`get`/`--strict`
are exercised against real API servers instead of fakes. It's excluded from
`just check` (heavier: needs Docker, ~1-2 min) and runs as its own parallel
job in CI. Tests live under `e2e/` behind a `//go:build e2e` tag, so they
never run as part of `go test ./...`. For iterating on a single test, run
`just e2e-up` once and then `just e2e-test` repeatedly against the same
warm clusters; `just e2e-down` when done.

`demo.gif` in the README is a staged recording (`demo.tape` + the canned
`demo/kubectl-fleet` stub), not live cluster output: there's no multi-region
prod fleet to record against. It mirrors the sample blocks already in
README.md; regenerate it with `just demo` if those samples change.

`just check` is the gate. Every commit and every PR must leave the tree green.

## Project layout

```
cmd/kubectl-fleet/main.go    Entry; ldflag-injected version/commit/date
cmd/gendocs/main.go          Generates docs/ from the cobra command tree
internal/cmd/                Cobra subcommands; one verb per file
internal/kubeconfig/         Multi-context loader + REST config builder
internal/fleet/              Bounded parallel executor (errgroup with SetLimit)
internal/k8s/                Typed + dynamic + discovery client factory; GVR resolver
internal/output/             Table/JSON/YAML printers
internal/health/             Per-cluster summary used by `status`
docs/                        Generated command reference (see `just docs`)
e2e/                         Black-box tests against real kind clusters (see `just e2e`)
```

New subcommands live in `internal/cmd/`, register in `NewRootCmd` (`root.go`), and
accept `*genericclioptions.ConfigFlags` so kubectl global flags (`--context`,
`--namespace`, `--kubeconfig`) work uniformly.

## Coding conventions

- **Error strings**: lowercase, no trailing punctuation, wrapped with `%w`.
  Example: `fmt.Errorf("resolve gvr: %w", err)`, not `"Failed to resolve GVR."`
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
just release-snapshot    # local goreleaser dry-run: verify multi-arch builds
just release 0.1.1       # creates tag v0.1.1, pushes, fires release workflow
```

Goreleaser runs in GitHub Actions on tag push, produces multi-arch archives
(linux/darwin/windows × amd64/arm64) plus a `checksums.txt`, and attaches them
to the corresponding GitHub Release. Release notes are auto-generated from
commits between tags, grouped by Conventional-Commit type.

Versioning follows SemVer:

- `v0.x.y`: pre-1.0, breaking changes allowed in minor bumps
- Patch bumps for fixes and non-breaking polish between phases
- Minor bumps for new phases (Argo support, drift detection, etc.)
- `v1.0.0` will mark feature-complete and trigger krew submission

## Reporting issues

Open a GitHub issue with:

- `kubectl fleet version` output
- `kubectl version` output
- Minimal reproduction (kubeconfig snippet, command line, observed vs expected)
- For multi-cluster bugs: number of contexts and any regex used

Security issues: see [SECURITY.md](SECURITY.md). Do not open a public issue.
