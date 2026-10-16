package fakes

import (
	"context"
	"net"
	"slices"
	"strings"
)

// DNSCall is one call to DNS.
type DNSCall struct {
	Meta
	// Name is the host passed to LookupCNAME or the domain passed to
	// LookupMX, exactly as given.
	Name string
	// CNAME and MX are what the lookup returned, once Done with no error.
	CNAME string
	MX    []net.MX
}

func (c *DNSCall) clone() DNSCall {
	d := *c
	d.MX = slices.Clone(c.MX)
	return d
}

// DNS is a recording dnsx.Resolver whose answers a test programs per name.
// A name with no programmed answer of the type asked for returns a
// *net.DNSError with IsNotFound set, as a real lookup of a missing name
// does.
//
// Names match as DNS names do: without regard to case (RFC 4343) and with
// or without the trailing dot of an absolute name. Answers are returned
// exactly as programmed, so a test that mirrors a real resolver programs a
// CNAME target with its trailing dot ("zone.b-cdn.net.").
//
// The fake doesn't model address records or CNAME chains. To mirror a real
// resolver, a test programs LookupCNAME's final answer: the end of the
// chain, or for a host with address records but no CNAME the host's own
// name with a trailing dot (a real lookup returns that, not an error). A
// null MX is SetMX(domain, &net.MX{Host: ".", Pref: 0}).
type DNS struct {
	recorder[DNSCall, *DNSCall]
	cnames map[string]string
	mxs    map[string][]net.MX
}

// SetCNAME programs the answer for LookupCNAME(host). An empty target
// removes it.
func (d *DNS) SetCNAME(host, target string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cnames == nil {
		d.cnames = map[string]string{}
	}
	if target == "" {
		delete(d.cnames, dnsKey(host))
		return
	}
	d.cnames[dnsKey(host)] = target
}

// SetMX programs the answer for LookupMX(domain). LookupMX returns the
// records sorted by preference, as (*net.Resolver).LookupMX does; records
// with equal preference keep the order given. No records removes the
// answer.
func (d *DNS) SetMX(domain string, records ...*net.MX) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.mxs == nil {
		d.mxs = map[string][]net.MX{}
	}
	if len(records) == 0 {
		delete(d.mxs, dnsKey(domain))
		return
	}
	mx := make([]net.MX, len(records))
	for i, r := range records {
		mx[i] = *r
	}
	slices.SortStableFunc(mx, func(a, b net.MX) int { return int(a.Pref) - int(b.Pref) })
	d.mxs[dnsKey(domain)] = mx
}

// LookupCNAME returns the programmed answer for host.
func (d *DNS) LookupCNAME(ctx context.Context, host string) (string, error) {
	c := d.do(ctx, "LookupCNAME", DNSCall{Name: host}, func(c *DNSCall) error {
		t, ok := d.cnames[dnsKey(host)]
		if !ok {
			return notFound(host)
		}
		c.CNAME = t
		return nil
	})
	return c.CNAME, c.Err
}

// LookupMX returns copies of the programmed records for domain.
func (d *DNS) LookupMX(ctx context.Context, domain string) ([]*net.MX, error) {
	c := d.do(ctx, "LookupMX", DNSCall{Name: domain}, func(c *DNSCall) error {
		mx, ok := d.mxs[dnsKey(domain)]
		if !ok {
			return notFound(domain)
		}
		c.MX = slices.Clone(mx)
		return nil
	})
	if c.Err != nil {
		return nil, c.Err
	}
	out := make([]*net.MX, len(c.MX))
	for i := range c.MX {
		r := c.MX[i]
		out[i] = &r
	}
	return out, nil
}

func dnsKey(name string) string {
	return strings.ToLower(strings.TrimSuffix(name, "."))
}

func notFound(name string) error {
	return &net.DNSError{Err: "no such host", Name: name, IsNotFound: true}
}
