package cmd

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestEveryCommandHasLongAndExamples(t *testing.T) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			walk(sub)
		}
		if c.Hidden || c.Name() == "help" || c.Name() == "completion" || (c.HasParent() && c.Parent().Name() == "completion") {
			return
		}
		if strings.TrimSpace(c.Long) == "" {
			t.Errorf("%q has no Long description", c.CommandPath())
		}
		if strings.TrimSpace(c.Example) == "" {
			t.Errorf("%q has no examples", c.CommandPath())
		}
		for _, line := range strings.Split(c.Example, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if !strings.HasPrefix(line, "kubectl fleet") {
				t.Errorf("%q example %q should start with \"kubectl fleet\"", c.CommandPath(), line)
			}
		}
	}
	walk(NewRootCmd())
}
