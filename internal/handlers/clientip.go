package handlers

import (
	"context"
	"net/http"
	"strings"
)

// clientIPKey is the context key under which ipAllowGuard stashes the
// resolved client IP. Using a typed, unexported key prevents collisions
// with other packages' context values.
type clientIPKey struct{}

// withClientIP returns a child context carrying the resolved client IP.
// Called once by ipAllowGuard after the ACL has decided what the real
// client IP is (RemoteAddr, or XFF-from-trusted-proxy).
func withClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPKey{}, ip)
}

// clientIP reads the resolved client IP from the request context.
// Falls back to a best-effort parse of r.RemoteAddr for any request
// that didn't pass through ipAllowGuard (in practice: tests).
func clientIP(r *http.Request) string {
	if ip, ok := r.Context().Value(clientIPKey{}).(string); ok && ip != "" {
		return ip
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return strings.Trim(host, "[]")
}
