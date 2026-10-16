package app_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"meet.space/internal/testkit/apptest"
)

// Security Rule 8 (decisions.md "Client IP") as the app is wired from its
// configuration: TRUSTED_PROXIES decides whose forwarded header is
// believed, CLIENT_IP_HEADER is the only header read, and with no trusted
// proxies (dev) the peer is always used.
func TestRule8_AppWiresTrustedProxiesAndHeaderFromConfig(t *testing.T) {
	req := func(peer string, hdr map[string]string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "http://"+apptest.PlatformHost+"/", nil)
		r.RemoteAddr = peer
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		return r
	}
	a := apptest.NewApp(t, apptest.WithEnv("TRUSTED_PROXIES", "10.0.0.0/8"), apptest.WithEnv("CLIENT_IP_HEADER", "Cdn-Client-Ip"))
	cases := []struct {
		name string
		peer string
		hdr  map[string]string
		want string
	}{
		{"trusted peer, configured header", "10.0.0.2:9000", map[string]string{"Cdn-Client-Ip": "198.51.100.7"}, "198.51.100.7"},
		{"untrusted peer, configured header", "203.0.113.5:4444", map[string]string{"Cdn-Client-Ip": "198.51.100.7"}, "203.0.113.5"},
		{"trusted peer, other header", "10.0.0.2:9000", map[string]string{"X-Forwarded-For": "198.51.100.8"}, "10.0.0.2"},
	}
	for _, c := range cases {
		if got := a.App.ClientIP(req(c.peer, c.hdr)); got != netip.MustParseAddr(c.want) {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
	dev := apptest.NewApp(t) // TRUSTED_PROXIES unset
	if got := dev.App.ClientIP(req("10.0.0.2:9000", map[string]string{"X-Forwarded-For": "198.51.100.7"})); got != netip.MustParseAddr("10.0.0.2") {
		t.Errorf("no trusted proxies: got %s, want the peer", got)
	}
}
