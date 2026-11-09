// Command gendocs generates the markdown command reference into ./docs
// (`just docs`, committed). With -man DIR it writes man pages there instead
// (the release build, which ships them in the archives: a man page carries
// a date, so committing them would churn).
package main

import (
	"bytes"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/spf13/cobra/doc"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/cmd"
)

func main() {
	man := flag.String("man", "", "write man pages (section 1) to this directory instead of markdown to ./docs")
	flag.Parse()
	root := cmd.NewRootCmd()
	root.DisableAutoGenTag = true

	if *man != "" {
		if err := os.MkdirAll(*man, 0o755); err != nil {
			log.Fatal(err)
		}
		header := &doc.GenManHeader{Title: "KUBECTL-FLEET", Section: "1", Source: "kubectl-fleet", Manual: "kubectl-fleet manual"}
		if err := doc.GenManTree(root, header, *man); err != nil {
			log.Fatal(err)
		}
		if err := normalizeHomeDir(*man); err != nil {
			log.Fatal(err)
		}
		return
	}

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
