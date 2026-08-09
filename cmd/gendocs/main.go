// Command gendocs generates markdown command reference docs for
// kubectl-fleet into ./docs. Run via `just docs`.
package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"

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
	if err := normalizeHomeDir("docs"); err != nil {
		log.Fatal(err)
	}
}

// normalizeHomeDir rewrites the machine-specific $HOME baked into generated
// flag defaults (--cache-dir, via genericclioptions.ConfigFlags) so docs/ is
// reproducible across machines and CI runners instead of diffing on every
// regeneration.
func normalizeHomeDir(dir string) error {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		replaced := bytes.ReplaceAll(b, []byte(home), []byte("$HOME"))
		if !bytes.Equal(replaced, b) {
			if err := os.WriteFile(path, replaced, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}
