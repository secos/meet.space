// Package middleware holds the request checks every route goes through:
// the Origin check (Security Rule 5), client IP extraction (Rule 8), the
// cache and referrer headers (Rule 7, route categories) and the security
// headers sent on every response.
package middleware

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// Security headers sent on every response (decisions.md "Security
// headers"): browsers must not guess a file's type, and no site may show
// our pages in a frame.
const (
	NoSniffValue        = "nosniff"
	FrameAncestorsValue = "frame-ancestors 'none'"
	FrameOptionsValue   = "DENY"
)

// SetSecurityHeaders writes the headers above into h, replacing whatever
// a handler put there.
func SetSecurityHeaders(h http.Header) {
	h.Set("X-Content-Type-Options", NoSniffValue)
	h.Set("Content-Security-Policy", FrameAncestorsValue)
	h.Set("X-Frame-Options", FrameOptionsValue)
}

// ErrOriginMismatch is returned when a state-changing request did not come
// from the site it was sent to.
var ErrOriginMismatch = errors.New("origin check failed")

// CheckOrigin applies Security Rule 5 as decided in decisions.md ("Origin
// check (Rule 5) details"): Origin, when present, must name currentHost;
// when absent, Referer must; when neither is present the request is
// refused. Only host[:port] is compared, never the scheme, and hosts are
// compared after parsing, never by prefix.
func CheckOrigin(r *http.Request, currentHost string) error {
	if currentHost == "" {
		return ErrOriginMismatch
	}
	if vals := r.Header.Values("Origin"); len(vals) > 0 {
		if len(vals) == 1 && sameHost(vals[0], currentHost) {
			return nil
		}
		return ErrOriginMismatch
	}
	if vals := r.Header.Values("Referer"); len(vals) > 0 {
		if len(vals) == 1 && sameHost(vals[0], currentHost) {
			return nil
		}
		return ErrOriginMismatch
	}
	return ErrOriginMismatch
}

// sameHost reports whether the absolute http(s) URL raw names host. A URL
// with userinfo is refused outright.
func sameHost(raw, host string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User != nil || u.Opaque != "" {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	return u.Host != "" && strings.ToLower(u.Host) == host
}

// OriginRefusedBody is the plain, unbranded body of a refused request
// (decisions.md: the same on every host, no platform or group name).
const OriginRefusedBody = "This request didn't come from this site. Go back, reload the page and try again.\n"

// WriteOriginRefused sends the 403 for a failed Origin check.
func WriteOriginRefused(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(OriginRefusedBody))
}

// ClientIP returns the visitor's address under Security Rule 8 as decided
// in decisions.md ("Client IP"): the forwarded header is trusted only when
// the immediate peer is a trusted proxy, and then its rightmost address
// that is not itself a trusted proxy is used. Anything missing or malformed
// falls back to the peer address.
func ClientIP(r *http.Request, trusted []netip.Prefix, header string) netip.Addr {
	peer := peerAddr(r.RemoteAddr)
	if !peer.IsValid() || !inPrefixes(peer, trusted) || header == "" {
		return peer
	}
	var entries []string
	for _, v := range r.Header.Values(header) {
		entries = append(entries, strings.Split(v, ",")...)
	}
	for i := len(entries) - 1; i >= 0; i-- {
		a, ok := parseForwardedAddr(entries[i])
		if !ok {
			return peer
		}
		if !inPrefixes(a, trusted) {
			return a
		}
	}
	return peer
}

func peerAddr(remote string) netip.Addr {
	if ap, err := netip.ParseAddrPort(remote); err == nil {
		return ap.Addr().Unmap()
	}
	if host, _, err := net.SplitHostPort(remote); err == nil {
		remote = host
	}
	a, err := netip.ParseAddr(remote)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}

// parseForwardedAddr accepts a bare IPv4 or IPv6 address, optionally with
// a port ("1.2.3.4:5", "[::1]:5"). Anything else is malformed.
func parseForwardedAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}, false
	}
	if ap, err := netip.ParseAddrPort(s); err == nil {
		if ap.Addr().Zone() != "" {
			return netip.Addr{}, false
		}
		return ap.Addr().Unmap(), true
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

func inPrefixes(a netip.Addr, ps []netip.Prefix) bool {
	for _, p := range ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

type clientIPKey struct{}

// WithClientIP returns ctx carrying the request's client IP.
func WithClientIP(ctx context.Context, a netip.Addr) context.Context {
	return context.WithValue(ctx, clientIPKey{}, a)
}

// ClientIPFrom returns the client IP stored by the router.
func ClientIPFrom(ctx context.Context) netip.Addr {
	a, _ := ctx.Value(clientIPKey{}).(netip.Addr)
	return a
}
