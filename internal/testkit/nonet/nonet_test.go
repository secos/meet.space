package nonet_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"meet.space/internal/testkit/nonet"
)

// P0-HARNESS-08: the suite never touches the network. With the guard in
// place an HTTP request to a public address and a DNS lookup both fail
// without leaving the machine; loopback still works (in-process servers).
func TestGuardBlocksTheNetwork(t *testing.T) {
	_, err := http.Get("http://192.0.2.1/") // TEST-NET-1, never routed
	if !errors.Is(err, nonet.ErrNetworkInTest) {
		t.Errorf("HTTP to a public address: %v", err)
	}
	if _, err := net.DefaultResolver.LookupHost(context.Background(), "example.com"); err == nil {
		t.Error("DNS lookup succeeded")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	res, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("loopback request refused: %v", err)
	}
	res.Body.Close()
}
