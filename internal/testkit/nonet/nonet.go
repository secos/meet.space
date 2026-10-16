// Package nonet stops tests from touching the network. Importing it (any
// test package does, through testkit) replaces the default HTTP transport
// and resolver before any test runs.
package nonet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
)

// ErrNetworkInTest is returned by the guards below. The suite never touches
// the network; the only connection a test may open is to the local
// Postgres, which pgx dials itself.
var ErrNetworkInTest = errors.New("testkit: network access is not allowed in tests")

// Every test package imports testkit, so these guards are in place before
// any test runs: the default HTTP transport refuses anything but loopback
// addresses, and the default resolver can't reach a DNS server.
func init() {
	http.DefaultTransport = &http.Transport{DialContext: loopbackOnly}
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			return nil, fmt.Errorf("%w: DNS lookup via %s", ErrNetworkInTest, address)
		},
	}
}

func loopbackOnly(ctx context.Context, network, address string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ip, err := netip.ParseAddr(host)
	if err != nil || !ip.IsLoopback() {
		return nil, fmt.Errorf("%w: dial %s", ErrNetworkInTest, address)
	}
	var d net.Dialer
	return d.DialContext(ctx, network, address)
}
