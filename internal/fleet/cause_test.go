package fleet

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"syscall"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
)

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestCause(t *testing.T) {
	gr := schema.GroupResource{Resource: "pods"}
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"deadline", fmt.Errorf("list pods: %w", context.DeadlineExceeded), "timed out"},
		{"net timeout", &url.Error{Op: "Get", URL: "https://x", Err: timeoutErr{}}, "timed out"},
		{"refused", &url.Error{Op: "Get", URL: "https://x", Err: &net.OpError{Op: "dial", Err: syscall.ECONNREFUSED}}, "connection refused"},
		{"dns", &url.Error{Op: "Get", URL: "https://x", Err: &net.DNSError{Err: "no such host", Name: "x"}}, "DNS lookup failed"},
		{"tls", &url.Error{Op: "Get", URL: "https://x", Err: x509.UnknownAuthorityError{}}, "TLS/certificate error"},
		{"unauthorized", fmt.Errorf("list: %w", apierrors.NewUnauthorized("no")), "unauthorized"},
		{"forbidden", fmt.Errorf("list: %w", apierrors.NewForbidden(gr, "", errors.New("rbac"))), "forbidden"},
		{"kind", fmt.Errorf(`kind "widgets" %w`, k8s.ErrKindNotFound), "kind not found"},
		{"other", errors.New("boom"), "other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Cause(tc.err); got != tc.want {
				t.Errorf("Cause(%v)=%q want %q", tc.err, got, tc.want)
			}
		})
	}
}
