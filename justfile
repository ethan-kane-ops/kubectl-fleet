e2e_cluster_a := "fleet-e2e-a"
e2e_cluster_b := "fleet-e2e-b"
# Pinned by digest for reproducible clusters; see kind's v0.32.0 release notes
# for the full published image list.
e2e_node_image := "kindest/node:v1.36.1@sha256:3489c7674813ba5d8b1a9977baea8a6e553784dab7b84759d1014dbd78f7ebd5"
e2e_kubeconfig := "e2e/.kubeconfig"

default:
    @just --list

# Build the binary into ./bin/ (isolated, does not affect installed binary)
build:
    go build -o bin/kubectl-fleet ./cmd/kubectl-fleet

# Run the locally built binary via kubectl plugin protocol (PATH-prepended)
run *args: build
    PATH="$(pwd)/bin:$PATH" kubectl fleet {{args}}

# Run all tests
test:
    go test ./...

# Run tests with race detector
test-race:
    go test -race ./...

# Run linters
lint:
    go vet ./...
    golangci-lint run

# Tidy go modules
tidy:
    go mod tidy

# Tidy + lint + test
check: tidy lint test

# Remove build artifacts
clean:
    rm -rf bin/ dist/

# Install binary via `go install` so kubectl discovers it on PATH
install:
    go install ./cmd/kubectl-fleet
    mise reshim 2>/dev/null || true
    @echo "installed → $(which kubectl-fleet 2>/dev/null || go env GOBIN)/kubectl-fleet"

# Regenerate command reference docs into ./docs
docs:
    go run ./cmd/gendocs

# Regenerate the README demo gif (requires vhs: https://github.com/charmbracelet/vhs)
demo:
    vhs demo.tape

# Create the two local kind clusters used for e2e testing and apply fixtures (requires kind + Docker)
e2e-up:
    #!/usr/bin/env bash
    set -euo pipefail
    for name in {{ e2e_cluster_a }} {{ e2e_cluster_b }}; do
        if ! kind get clusters | grep -qx "$name"; then
            kind create cluster --name "$name" --image {{ e2e_node_image }} --wait 90s
        fi
    done
    kind get kubeconfig --name {{ e2e_cluster_a }} > /tmp/kubectl-fleet-e2e-a.kubeconfig
    kind get kubeconfig --name {{ e2e_cluster_b }} > /tmp/kubectl-fleet-e2e-b.kubeconfig
    KUBECONFIG=/tmp/kubectl-fleet-e2e-a.kubeconfig:/tmp/kubectl-fleet-e2e-b.kubeconfig kubectl config view --flatten > {{ e2e_kubeconfig }}
    kubectl --kubeconfig {{ e2e_kubeconfig }} --context kind-{{ e2e_cluster_a }} apply -f e2e/fixtures/healthy.yaml
    kubectl --kubeconfig {{ e2e_kubeconfig }} --context kind-{{ e2e_cluster_b }} apply -f e2e/fixtures/crashloop.yaml

# Run the e2e suite against clusters created by `just e2e-up`
e2e-test: build
    FLEET_BINARY="$(pwd)/bin/kubectl-fleet" FLEET_E2E_KUBECONFIG="$(pwd)/{{ e2e_kubeconfig }}" go test -tags e2e -count=1 -v ./e2e/...

# Delete the e2e kind clusters
e2e-down:
    kind delete cluster --name {{ e2e_cluster_a }}
    kind delete cluster --name {{ e2e_cluster_b }}
    rm -f {{ e2e_kubeconfig }}

# Full e2e loop: create clusters, run tests, always tear down after (what CI runs)
e2e: e2e-up
    #!/usr/bin/env bash
    set -uo pipefail
    trap 'just e2e-down' EXIT
    just e2e-test

# Dry-run a goreleaser build locally (no publish)
release-snapshot:
    goreleaser release --snapshot --clean

# Cut a release: tag and push. release.yml workflow runs goreleaser.
release version:
    git tag v{{version}}
    git push origin v{{version}}
