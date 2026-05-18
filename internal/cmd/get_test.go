package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetCmdArgs(t *testing.T) {
	c := newGetCmd(newFlags(tmpKubeconfig(t)))
	c.SetOut(&bytes.Buffer{})
	c.SetErr(&bytes.Buffer{})
	c.SetArgs([]string{})
	if err := c.Execute(); err == nil {
		t.Fatal("expected error when called with no args")
	}
}

func TestGetCmdNoMatchingContexts(t *testing.T) {
	c := newGetCmd(newFlags(tmpKubeconfig(t)))
	var buf bytes.Buffer
	c.SetOut(&buf)
	c.SetErr(&buf)
	c.SetArgs([]string{"pods", "--contexts", "does-not-match"})
	err := c.Execute()
	if err == nil || !strings.Contains(err.Error(), "no matching contexts") {
		t.Fatalf("expected no-matching-contexts error, got %v", err)
	}
}

func TestAgeOf(t *testing.T) {
	now := time.Now()
	cases := []struct {
		offset time.Duration
		want   string
	}{
		{time.Second * 10, "10s"},
		{time.Minute * 5, "5m"},
		{time.Hour * 3, "3h"},
		{time.Hour * 48, "2d"},
	}
	for _, c := range cases {
		ts := metav1.NewTime(now.Add(-c.offset))
		got := ageOf(ts)
		if got != c.want {
			t.Errorf("offset=%s got=%q want=%q", c.offset, got, c.want)
		}
	}
	if ageOf(metav1.Time{}) != "" {
		t.Error("zero time should yield empty string")
	}
}
