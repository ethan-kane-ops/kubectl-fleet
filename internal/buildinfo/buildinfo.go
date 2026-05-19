// Package buildinfo exposes build metadata injected at link time via
// `-ldflags -X` from the goreleaser pipeline. Values default to "dev"
// markers when the binary is built without ldflag injection (e.g. a plain
// `go build` during local development).
package buildinfo

// Version is the semver tag (e.g. "v0.1.0"). "dev" when unset.
var Version = "dev"

// Commit is the short git SHA. "none" when unset.
var Commit = "none"

// Date is the RFC3339 build timestamp. "unknown" when unset.
var Date = "unknown"
