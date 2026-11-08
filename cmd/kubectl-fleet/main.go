package main

import (
	"fmt"
	"os"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/cmd"
)

func main() {
	// client-go reports discovery failures through klog as well as returning
	// them. Each failure already reaches the user once as a per-context
	// "warn:" line, so the klog copy is noise.
	klog.SetLogger(logr.Discard())

	if err := cmd.NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
