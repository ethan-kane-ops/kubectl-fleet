package fleet

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"strings"
	"syscall"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/ethan-kane-ops/kubectl-fleet/internal/k8s"
)

// Cause classifies why a context failed, for grouping failures in the
// brief summary: timed out, connection refused, DNS lookup failed,
// TLS/certificate error, unauthorized, forbidden, kind not found or other.
func Cause(err error) string {
	var (
		dnsErr   *net.DNSError
		certErr  x509.UnknownAuthorityError
		hostErr  x509.HostnameError
		invalid  x509.CertificateInvalidError
		timeout  interface{ Timeout() bool }
		apiState apierrors.APIStatus
	)
	switch {
	case errors.Is(err, k8s.ErrKindNotFound):
		return "kind not found"
	case errors.As(err, &apiState) && apierrors.IsUnauthorized(err):
		return "unauthorized"
	case errors.As(err, &apiState) && apierrors.IsForbidden(err):
		return "forbidden"
	case errors.As(err, &apiState) && apierrors.IsTimeout(err):
		return "timed out"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, os.ErrDeadlineExceeded),
		errors.As(err, &timeout) && timeout.Timeout():
		return "timed out"
	case errors.As(err, &dnsErr):
		return "DNS lookup failed"
	case errors.Is(err, syscall.ECONNREFUSED):
		return "connection refused"
	case errors.As(err, &certErr), errors.As(err, &hostErr), errors.As(err, &invalid),
		strings.Contains(errString(err), "x509:"), strings.Contains(errString(err), "tls:"):
		return "TLS/certificate error"
	}
	return "other"
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
