// Package dnsx is the interface to DNS lookups: the CNAME check when a
// custom domain is verified, and the MX check on an anonymous RSVP's
// email address. Code takes a Resolver instead of calling the net
// package's lookups, so tests answer from a programmable fake and never
// touch the network.
//
// The method set matches *net.Resolver's, so production passes
// net.DefaultResolver (or its own *net.Resolver) as is.
package dnsx

import (
	"context"
	"net"
)

// Resolver looks up the DNS records the app checks.
type Resolver interface {
	// LookupCNAME returns the canonical name for host, as
	// (*net.Resolver).LookupCNAME does: an absolute name with a trailing
	// dot, such as "zone.b-cdn.net.". That is the final name after
	// following every CNAME in a chain, and a host with address records
	// but no CNAME returns its own name with no error.
	LookupCNAME(ctx context.Context, host string) (string, error)

	// LookupMX returns the domain's MX records sorted by preference, as
	// (*net.Resolver).LookupMX does. A domain with no MX records returns
	// an error, as a domain that doesn't exist does.
	//
	// OPEN (Phase 2, the RSVP MX check): the documents say only "a syntax
	// and MX check". They don't say whether a domain with no MX but an
	// address record passes (RFC 5321 treats it as able to receive mail),
	// or whether a null MX ("." at preference 0, RFC 7505: the domain
	// accepts no mail) fails.
	LookupMX(ctx context.Context, domain string) ([]*net.MX, error)
}
