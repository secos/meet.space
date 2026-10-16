package fakes_test

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"

	"meet.space/internal/dnsx"
	"meet.space/internal/testkit/fakes"
)

// Production passes a *net.Resolver where a dnsx.Resolver is wanted.
var _ dnsx.Resolver = (*net.Resolver)(nil)

// Programmed answers are returned as given; a name with no answer of the
// requested type is a not-found *net.DNSError naming it; answers can change
// between lookups (the record is added after the first check).
//
// P0-HARNESS-06, P0-HARNESS-07
func TestDNS_AnswersAsProgrammedPerName(t *testing.T) {
	ctx := context.Background()
	d := &fakes.DNS{}

	_, err := d.LookupCNAME(ctx, "community.example.com")
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound || dnsErr.Name != "community.example.com" {
		t.Fatalf("unknown name: %v, want a not-found *net.DNSError for it", err)
	}

	d.SetCNAME("community.example.com", "zone.b-cdn.net.")
	if got, err := d.LookupCNAME(ctx, "community.example.com"); err != nil || got != "zone.b-cdn.net." {
		t.Errorf("LookupCNAME = %q, %v", got, err)
	}
	if _, err := d.LookupMX(ctx, "community.example.com"); !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
		t.Errorf("MX of a name with only a CNAME: %v, want not found", err)
	}

	d.SetCNAME("community.example.com", "")
	if _, err := d.LookupCNAME(ctx, "community.example.com"); !errors.As(err, &dnsErr) || !dnsErr.IsNotFound {
		t.Errorf("after removing the answer: %v, want not found", err)
	}

	calls := d.Calls()
	if len(calls) != 4 || calls[1].Name != "community.example.com" || calls[1].CNAME != "zone.b-cdn.net." || calls[0].Err == nil {
		t.Errorf("calls = %+v", calls)
	}
}

// LookupMX returns records sorted by preference, as (*net.Resolver).LookupMX
// documents, and returns copies.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestDNS_MXSortedByPreferenceAndCopied(t *testing.T) {
	ctx := context.Background()
	d := &fakes.DNS{}
	d.SetMX("example.com",
		&net.MX{Host: "backup.example.com.", Pref: 20},
		&net.MX{Host: "mx1.example.com.", Pref: 10},
		&net.MX{Host: "mx2.example.com.", Pref: 10})
	got, err := d.LookupMX(ctx, "example.com")
	if err != nil {
		t.Fatal(err)
	}
	want := []*net.MX{{Host: "mx1.example.com.", Pref: 10}, {Host: "mx2.example.com.", Pref: 10}, {Host: "backup.example.com.", Pref: 20}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("LookupMX = %v, want %v", mxString(got), mxString(want))
	}
	got[0].Host = "evil.example."
	again, _ := d.LookupMX(ctx, "example.com")
	if again[0].Host != "mx1.example.com." {
		t.Errorf("changing a returned record changed the fake: %v", mxString(again))
	}
	if c := d.Calls()[0]; len(c.MX) != 3 || c.MX[0].Host != "mx1.example.com." {
		t.Errorf("recorded answer = %+v", c.MX)
	}
}

// DNS names are case-insensitive (RFC 4343) and the trailing dot of an
// absolute name doesn't make it a different name, so the fake matches a
// lookup the way a real resolver would. The call log keeps the name as
// the caller wrote it.
//
// P0-HARNESS-06, P0-HARNESS-07
func TestDNS_NamesMatchLikeDNSNames(t *testing.T) {
	ctx := context.Background()
	d := &fakes.DNS{}
	d.SetCNAME("Community.Example.com", "zone.b-cdn.net.")
	for _, name := range []string{"community.example.com", "COMMUNITY.EXAMPLE.COM.", "Community.Example.com"} {
		if got, err := d.LookupCNAME(ctx, name); err != nil || got != "zone.b-cdn.net." {
			t.Errorf("LookupCNAME(%q) = %q, %v", name, got, err)
		}
	}
	if c := d.Calls()[1]; c.Name != "COMMUNITY.EXAMPLE.COM." {
		t.Errorf("recorded name = %q, want it as given", c.Name)
	}
}

// A chosen lookup can fail with a chosen error, such as a temporary
// resolver failure, which is different from "not found".
//
// P0-HARNESS-06, P0-HARNESS-07
func TestDNS_FailWhenReturnsTheChosenError(t *testing.T) {
	ctx := context.Background()
	d := &fakes.DNS{}
	d.SetMX("example.com", &net.MX{Host: "mx.example.com.", Pref: 10})
	temp := &net.DNSError{Err: "server misbehaving", Name: "example.com", IsTemporary: true}
	d.FailWhen(func(c fakes.DNSCall) bool { return c.Method == "LookupMX" && c.MethodN == 1 }, temp)

	mx, err := d.LookupMX(ctx, "example.com")
	var dnsErr *net.DNSError
	if mx != nil || !errors.As(err, &dnsErr) || !dnsErr.IsTemporary || dnsErr.IsNotFound {
		t.Errorf("first lookup = %v, %v; want the temporary error", mx, err)
	}
	if mx, err := d.LookupMX(ctx, "example.com"); err != nil || len(mx) != 1 {
		t.Errorf("second lookup = %v, %v", mx, err)
	}
}

func mxString(mx []*net.MX) []net.MX {
	out := make([]net.MX, len(mx))
	for i, m := range mx {
		out[i] = *m
	}
	return out
}
