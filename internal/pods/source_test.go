package pods

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func TestRowsMissingStatusColumn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"kind":"Table","apiVersion":"meta.k8s.io/v1","metadata":{},` +
			`"columnDefinitions":[{"name":"Name"},{"name":"Restarts"}],"rows":[{"cells":["a","0"]}]}`))
	}))
	defer srv.Close()
	cfg := &rest.Config{Host: srv.URL}
	src, err := NewSource(cfg, kubernetes.NewForConfigOrDie(cfg))
	if err != nil {
		t.Fatal(err)
	}
	err = src.Rows(context.Background(), SelectRunning, func(string, string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "no STATUS column") {
		t.Errorf("want a missing STATUS column error, got %v", err)
	}
}
