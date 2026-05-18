// Package netacl enforces the dashboard's IP-allowlist policy. The
// public surface is small: build one ACL with New(), call Reload() when
// the network config changes, and from the request middleware call
// ClientIP() then Allowed(). Per the security spec, ALL access decisions
// are based on the client IP — request hostnames are never trusted.
//
// Allowlist composition (after preInit):
//
//   127.0.0.0/8 + ::1/128   — always allowed (operator can recover via
//                              `docker exec` even after a misconfiguration)
//   user-configured CIDRs   — added via /config/network (LAN subnets,
//                              reverse-proxy upstream IPs, etc.)
package netacl

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
)

// Hard-coded always-allowed networks.
var (
	loopback4 = mustCIDR("127.0.0.0/8")
	loopback6 = mustCIDR("::1/128")
)

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// ACL holds the live allowlist. Reload() swaps the entire state under
// a mutex; the per-request path reads through an atomic pointer so
// hot-path requests never block on a save.
//
// preInit is a separate atomic so the bootstrap (wide-open during the
// 5-minute setup window) can be flipped without rebuilding the snapshot.
type ACL struct {
	mu       sync.Mutex
	snapshot atomic.Pointer[snapshot]
	preInit  atomic.Bool
}

// snapshot is the immutable view of the current allowlist. A new value
// is stored atomically on every Reload.
type snapshot struct {
	cidrs          []*net.IPNet
	trustedProxies []net.IP
}

// New returns an ACL in preInit mode (allows everything until a real
// config is loaded). The router flips preInit off once /config/.initialized
// exists and the plaintext netacl.json has been loaded.
func New() *ACL {
	a := &ACL{}
	a.snapshot.Store(&snapshot{})
	a.preInit.Store(true)
	return a
}

// SetPreInit toggles the bootstrap-allow-everything phase.
func (a *ACL) SetPreInit(v bool) { a.preInit.Store(v) }

// ReloadResult reports what Reload() observed during proxy DNS lookup
// so the network-config page can surface failures. Not all fields are
// always populated.
type ReloadResult struct {
	ResolvedProxyIPs []string
	ProxyNote        string
}

// Reload parses the supplied CIDRs, resolves the proxy hostname (DNS
// happens here, NOT at request time per spec), and atomically replaces
// the snapshot. Returns:
//
//   - non-nil error if any user-supplied CIDR is malformed (no swap happens);
//   - ReloadResult describing the resolution outcome.
//
// An unresolvable proxy hostname is not fatal — the proxy upstream
// simply stays disabled until the next save.
func (a *ACL) Reload(cidrs []string, proxyHost string) (ReloadResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	parsed, err := parseCIDRs(cidrs)
	if err != nil {
		return ReloadResult{}, err
	}

	res := ReloadResult{}
	proxyHost = strings.TrimSpace(proxyHost)
	var proxies []net.IP
	if proxyHost != "" {
		proxies = resolveProxy(proxyHost, &res)
	}

	a.snapshot.Store(&snapshot{
		cidrs:          parsed,
		trustedProxies: proxies,
	})
	return res, nil
}

func parseCIDRs(raw []string) ([]*net.IPNet, error) {
	var out []*net.IPNet
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		n, err := parseCIDR(s)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", s, err)
		}
		out = append(out, n)
	}
	return out, nil
}

func resolveProxy(host string, res *ReloadResult) []net.IP {
	// Accept a literal IP without a DNS round-trip.
	if ip := net.ParseIP(host); ip != nil {
		res.ResolvedProxyIPs = []string{ip.String()}
		return []net.IP{ip}
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		res.ProxyNote = "resolve failed: " + err.Error()
		return nil
	}
	for _, ip := range ips {
		res.ResolvedProxyIPs = append(res.ResolvedProxyIPs, ip.String())
	}
	return ips
}

// parseCIDR accepts either a bare IP ("1.2.3.4" → /32 or /128) or a
// fully-specified CIDR ("1.2.3.0/24").
func parseCIDR(s string) (*net.IPNet, error) {
	if !strings.Contains(s, "/") {
		ip := net.ParseIP(s)
		if ip == nil {
			return nil, errors.New("not an IP")
		}
		if ip4 := ip.To4(); ip4 != nil {
			return &net.IPNet{IP: ip4, Mask: net.CIDRMask(32, 32)}, nil
		}
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}, nil
	}
	_, n, err := net.ParseCIDR(s)
	return n, err
}

// ClientIP returns the IP that the allowlist should be applied to:
//
//   - if r.RemoteAddr is a trusted proxy, return the left-most X-Forwarded-For;
//   - otherwise return r.RemoteAddr's IP.
//
// Returns ok=false to instruct the caller to reject the request — that
// happens when a trusted-proxy connection arrived without a usable XFF
// header (per spec), or when RemoteAddr couldn't be parsed at all.
func (a *ACL) ClientIP(r *http.Request) (string, bool) {
	remote := remoteAddrIP(r.RemoteAddr)
	if remote == nil {
		return "", false
	}

	snap := a.snapshot.Load()
	if isTrustedProxy(snap.trustedProxies, remote) {
		xff := r.Header.Get("X-Forwarded-For")
		if xff == "" {
			return "", false
		}
		first := strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
		if net.ParseIP(first) == nil {
			return "", false
		}
		return first, true
	}
	return remote.String(), true
}

func isTrustedProxy(proxies []net.IP, ip net.IP) bool {
	for _, p := range proxies {
		if p.Equal(ip) {
			return true
		}
	}
	return false
}

// Allowed reports whether ip is on the active allowlist. During preInit
// it returns true unconditionally (the 5-minute setup window has its own
// guard mechanism).
func (a *ACL) Allowed(ip string) bool {
	if a.preInit.Load() {
		return true
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return false
	}
	// Always-allowed: loopback. A misconfigured operator can recover via
	// `docker exec` -> localhost.
	if loopback4.Contains(parsed) || loopback6.Contains(parsed) {
		return true
	}
	snap := a.snapshot.Load()
	for _, n := range snap.cidrs {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

// remoteAddrIP extracts the IP portion from a "host:port" RemoteAddr.
// SplitHostPort handles bracketed IPv6 correctly.
func remoteAddrIP(addr string) net.IP {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return net.ParseIP(host)
}
