package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/buildinfo"
)

func TestVersionCmd_clientOnly(t *testing.T) {
	c := newVersionCmd(newFlags(tmpKubeconfig(t)))
	var out, errBuf bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&errBuf)
	c.SetArgs([]string{"--client"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	body := out.String()
	if !strings.Contains(body, "kubectl-fleet") {
		t.Fatalf("missing kubectl-fleet component, got:\n%s", body)
	}
	if !strings.Contains(body, buildinfo.Version) {
		t.Fatalf("missing client version %q, got:\n%s", buildinfo.Version, body)
	}
}

func TestVersionCmd_clientOnlyJSON(t *testing.T) {
	c := newVersionCmd(newFlags(tmpKubeconfig(t)))
	var out bytes.Buffer
	c.SetOut(&out)
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"--client", "-o", "json"})
	if err := c.Execute(); err != nil {
		t.Fatalf("execute: %v", err)
	}
	var rows []map[string]string
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out.String())
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (client only), got %d", len(rows))
	}
	if rows[0]["COMPONENT"] != "kubectl-fleet" {
		t.Errorf("COMPONENT = %q", rows[0]["COMPONENT"])
	}
	if rows[0]["VERSION"] != buildinfo.Version {
		t.Errorf("VERSION = %q want %q", rows[0]["VERSION"], buildinfo.Version)
	}
}

func TestVersionCmd_noMatchingContexts(t *testing.T) {
	c := newVersionCmd(newFlags(tmpKubeconfig(t)))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{"--contexts", "does-not-match"})
	err := c.Execute()
	if err == nil || !strings.Contains(err.Error(), "no matching contexts") {
		t.Fatalf("expected no-matching-contexts error, got %v", err)
	}
}

func TestClientRow(t *testing.T) {
	cols, wide := clientRow()
	if cols[0] != "(client)" {
		t.Errorf("CONTEXT = %q", cols[0])
	}
	if cols[1] != "kubectl-fleet" {
		t.Errorf("COMPONENT = %q", cols[1])
	}
	if cols[2] != buildinfo.Version {
		t.Errorf("VERSION = %q want %q", cols[2], buildinfo.Version)
	}
	if wide[0] != buildinfo.Commit {
		t.Errorf("GIT_COMMIT = %q", wide[0])
	}
	if wide[1] != buildinfo.Date {
		t.Errorf("BUILD_DATE = %q", wide[1])
	}
}
