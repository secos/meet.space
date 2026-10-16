package nonet_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"meet.space/internal/testkit/nonet"
)

// P0-HARNESS-08: the guard itself refuses, not the network. Each refusal
// must carry ErrNetworkInTest, so a removed guard fails here even on a
// machine with no DNS or no route out (where TestGuardBlocksTheNetwork's
// "lookup failed" would still pass). A deadline keeps a broken guard
// from hanging the suite on a connect timeout.
func TestGuardRefusesItself(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// DNS: net.DNSError doesn't unwrap the dial error, so match its text.
	_, err := net.DefaultResolver.LookupHost(ctx, "example.com")
	if err == nil || !strings.Contains(err.Error(), nonet.ErrNetworkInTest.Error()) {
		t.Errorf("DNS lookup wasn't refused by the guard: %v", err)
	}

	// The transport refuses a hostname itself, before any lookup.
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com/", nil)
	if _, err := http.DefaultClient.Do(req); !errors.Is(err, nonet.ErrNetworkInTest) {
		t.Errorf("HTTP to a hostname: %v", err)
	}

	// A public address is refused at once, not after a connect timeout.
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, "http://192.0.2.1/", nil)
	if _, err := http.DefaultClient.Do(req); !errors.Is(err, nonet.ErrNetworkInTest) {
		t.Errorf("HTTP to a public address: %v", err)
	}
}
