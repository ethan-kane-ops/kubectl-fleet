default:
    @just --list

# Build the binary into ./bin/ (isolated — does not affect installed binary)
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

# Dry-run a goreleaser build locally (no publish)
release-snapshot:
    goreleaser release --snapshot --clean

# Cut a release: tag and push. release.yml workflow runs goreleaser.
release version:
    git tag v{{version}}
    git push origin v{{version}}
