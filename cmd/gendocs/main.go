// Command gendocs generates markdown command reference docs for
// kubectl-fleet into ./docs. Run via `just docs`.
package main

import (
	"log"
	"os"

	"github.com/spf13/cobra/doc"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/cmd"
)

func main() {
	root := cmd.NewRootCmd()
	root.DisableAutoGenTag = true

	if err := os.MkdirAll("docs", 0o755); err != nil {
		log.Fatal(err)
	}
	if err := doc.GenMarkdownTree(root, "docs"); err != nil {
		log.Fatal(err)
	}
}
