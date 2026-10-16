package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"meet.space/internal/middleware"
	_ "meet.space/internal/testkit/nonet"
)

const host = "localhost:8080"

func post(origin, referer []string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, "http://"+host+"/x", nil)
	for _, o := range origin {
		r.Header.Add("Origin", o)
	}
	for _, rf := range referer {
		r.Header.Add("Referer", rf)
	}
	return r
}

// P0-SECURITY-01: an Origin naming the current host passes, whatever the
// scheme (TLS ends before the app; decisions.md "Origin check").
func TestRule5_SameHostOriginIsAllowed(t *testing.T) {
	for _, o := range []string{"http://localhost:8080", "https://localhost:8080", "http://LOCALHOST:8080"} {
		if err := middleware.CheckOrigin(post([]string{o}, nil), host); err != nil {
			t.Errorf("Origin %q refused: %v", o, err)
		}
	}
	if err := middleware.CheckOrigin(post([]string{"http://hikers.localhost:8080"}, nil), "hikers.localhost:8080"); err != nil {
		t.Errorf("custom domain's own origin refused: %v", err)
	}
}

// P0-SECURITY-02, -03: any other origin is refused, compared by parsed host,
// never by prefix or substring; Origin: null is refused.
func TestRule5_OtherOriginsAreRefused(t *testing.T) {
	for _, o := range []string{
		"null",
		"http://hikers.localhost:8080",    // another of our sites
		"http://origin.localhost:8080",    // the origin host
		"https://evil.test",               // a foreign site
		"http://localhost:9090",           // same name, other port
		"http://localhost",                // port missing
		"http://localhost:8080.evil.test", // look-alike suffix
		"http://xlocalhost:8080",
		"http://localhost:8080@evil.test",
		"http://user@localhost:8080",
		"localhost:8080", // not a URL
		"",
	} {
		if err := middleware.CheckOrigin(post([]string{o}, nil), host); err == nil {
			t.Errorf("Origin %q allowed", o)
		}
	}
	if err := middleware.CheckOrigin(post([]string{"http://localhost:8080"}, nil), "hikers.localhost:8080"); err == nil {
		t.Error("platform origin allowed on a custom domain")
	}
}

// P0-SECURITY-04: Referer is consulted only when Origin is absent.
func TestRule5_RefererDecidesOnlyWhenOriginIsAbsent(t *testing.T) {
	cases := []struct {
		name            string
		origin, referer []string
		allowed         bool
	}{
		{"same-host referer", nil, []string{"http://localhost:8080/groups/x?y=1"}, true},
		{"foreign referer", nil, []string{"https://evil.test/"}, false},
		{"referer with userinfo trick", nil, []string{"http://localhost:8080@evil.test/"}, false},
		{"referer naming host in path", nil, []string{"http://evil.test/http://localhost:8080/"}, false},
		{"origin wins over matching referer", []string{"https://evil.test"}, []string{"http://localhost:8080/"}, false},
		{"matching origin wins over foreign referer", []string{"http://localhost:8080"}, []string{"https://evil.test/"}, true},
	}
	for _, c := range cases {
		err := middleware.CheckOrigin(post(c.origin, c.referer), host)
		if (err == nil) != c.allowed {
			t.Errorf("%s: allowed=%v, want %v", c.name, err == nil, c.allowed)
		}
	}
}

// decisions.md "Origin check": neither header present → refused.
func TestRule5_RequestWithNeitherHeaderIsRefused(t *testing.T) {
	if err := middleware.CheckOrigin(post(nil, nil), host); err == nil {
		t.Fatal("request without Origin or Referer allowed")
	}
}

// Two Origin headers can't both equal the host; the check refuses them.
func TestRule5_RepeatedOriginIsRefused(t *testing.T) {
	if err := middleware.CheckOrigin(post([]string{"http://localhost:8080", "http://localhost:8080"}, nil), host); err == nil {
		t.Fatal("repeated Origin allowed")
	}
}

// P0-SECURITY-03 / decisions.md "Origin check": hosts are compared whole,
// never by prefix. The dev host's port makes look-alike hosts fail to
// parse at all, so this uses production-shaped hosts with no port, and
// ports that extend the dev port.
func TestRule5_HostIsComparedWholeNotByPrefix(t *testing.T) {
	cases := []struct{ from, current string }{
		{"https://meet.space.evil.test", "meet.space"},
		{"https://meet.spacex", "meet.space"},
		{"https://meet.spac", "meet.space"},
		{"https://meet.space:8443", "meet.space"},
		{"https://hikers.example.com.evil.test", "hikers.example.com"},
		{"http://localhost:80800", "localhost:8080"},
		{"http://hikers.localhost:80801", "hikers.localhost:8080"},
	}
	for _, c := range cases {
		if err := middleware.CheckOrigin(post([]string{c.from}, nil), c.current); err == nil {
			t.Errorf("Origin %q allowed on %q", c.from, c.current)
		}
		if err := middleware.CheckOrigin(post(nil, []string{c.from + "/"}), c.current); err == nil {
			t.Errorf("Referer %q allowed on %q", c.from+"/", c.current)
		}
	}
	for _, c := range [][2]string{{"https://meet.space", "meet.space"}, {"https://hikers.example.com", "hikers.example.com"}} {
		if err := middleware.CheckOrigin(post([]string{c[0]}, nil), c[1]); err != nil {
			t.Errorf("Origin %q refused on %q: %v", c[0], c[1], err)
		}
	}
}

// decisions.md "Origin check": an Origin that is present decides, and
// `null` fails. A null or empty Origin is not treated as missing, so a
// matching Referer can't rescue it.
func TestRule5_PresentButFailingOriginIsNotRescuedByReferer(t *testing.T) {
	for _, o := range []string{"null", ""} {
		if err := middleware.CheckOrigin(post([]string{o}, []string{"http://localhost:8080/groups/x"}), host); err == nil {
			t.Errorf("Origin %q allowed because the Referer matched", o)
		}
	}
}

// When Referer decides, every Referer value must name this host; a second,
// foreign value is refused, not ignored.
func TestRule5_RepeatedRefererWithForeignValueIsRefused(t *testing.T) {
	if err := middleware.CheckOrigin(post(nil, []string{"http://localhost:8080/", "https://evil.test/"}), host); err == nil {
		t.Fatal("matching Referer followed by a foreign one allowed")
	}
}

// P0-SECURITY-02 / decisions.md: the refusal is a 403 with the same plain,
// unbranded body everywhere.
func TestRule5_RefusalIsPlainForbidden(t *testing.T) {
	rec := httptest.NewRecorder()
	middleware.WriteOriginRefused(rec)
	if rec.Code != http.StatusForbidden {
		t.Errorf("status %d, want 403", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("Content-Type %q, want text/plain", ct)
	}
	if rec.Body.String() != "This request didn't come from this site. Go back, reload the page and try again.\n" {
		t.Errorf("body %q", rec.Body.String())
	}
}

func ipReq(remote string, headers map[string][]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://"+host+"/", nil)
	r.RemoteAddr = remote
	for k, vs := range headers {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	return r
}

var caddy = []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}

// P0-SECURITY-10: when the peer isn't a trusted proxy, every forwarded
// header is ignored and the peer address is used.
func TestRule8_UntrustedPeerIgnoresForwardedHeaders(t *testing.T) {
	r := ipReq("203.0.113.5:4444", map[string][]string{
		"X-Forwarded-For": {"198.51.100.1"}, "Forwarded": {"for=198.51.100.2"}, "X-Real-Ip": {"198.51.100.3"},
	})
	if got := middleware.ClientIP(r, caddy, "X-Forwarded-For"); got != netip.MustParseAddr("203.0.113.5") {
		t.Errorf("got %s, want the peer 203.0.113.5", got)
	}
	if got := middleware.ClientIP(ipReq("[2001:db8::7]:443", nil), nil, "X-Forwarded-For"); got != netip.MustParseAddr("2001:db8::7") {
		t.Errorf("IPv6 peer: got %s", got)
	}
}

// P0-SECURITY-11 / decisions.md "Client IP": from a trusted proxy, the
// rightmost address in the configured header that isn't itself trusted.
func TestRule8_TrustedPeerUsesRightmostUntrustedEntry(t *testing.T) {
	cases := []struct {
		xff  []string
		want string
	}{
		{[]string{"198.51.100.7"}, "198.51.100.7"},
		{[]string{"203.0.113.9, 198.51.100.7"}, "198.51.100.7"},           // client claim on the left is ignored
		{[]string{"203.0.113.9, 198.51.100.7, 10.1.2.3"}, "198.51.100.7"}, // trusted hops skipped
		{[]string{"203.0.113.9", "198.51.100.7"}, "198.51.100.7"},         // several header lines
		{[]string{"[2001:db8::9]:443"}, "2001:db8::9"},
		{[]string{"198.51.100.7:5000"}, "198.51.100.7"},
	}
	for _, c := range cases {
		r := ipReq("10.0.0.2:9000", map[string][]string{"X-Forwarded-For": c.xff})
		if got := middleware.ClientIP(r, caddy, "X-Forwarded-For"); got != netip.MustParseAddr(c.want) {
			t.Errorf("XFF %q: got %s, want %s", c.xff, got, c.want)
		}
	}
}

// Only the configured header is read, even from a trusted proxy.
func TestRule8_OnlyTheConfiguredHeaderIsRead(t *testing.T) {
	r := ipReq("10.0.0.2:9000", map[string][]string{"X-Real-Ip": {"198.51.100.3"}, "Forwarded": {"for=198.51.100.2"}})
	if got := middleware.ClientIP(r, caddy, "X-Forwarded-For"); got != netip.MustParseAddr("10.0.0.2") {
		t.Errorf("got %s, want the peer", got)
	}
	r = ipReq("10.0.0.2:9000", map[string][]string{"Cdn-Client-Ip": {"198.51.100.4"}, "X-Forwarded-For": {"198.51.100.1"}})
	if got := middleware.ClientIP(r, caddy, "Cdn-Client-Ip"); got != netip.MustParseAddr("198.51.100.4") {
		t.Errorf("custom header: got %s", got)
	}
}

// A missing, empty or malformed header falls back to the peer.
func TestRule8_MalformedHeaderFallsBackToPeer(t *testing.T) {
	for _, xff := range [][]string{nil, {""}, {"garbage"}, {"198.51.100.7, nope"}, {"10.1.1.1, 10.2.2.2"}, {"fe80::1%eth0"}, {"[fe80::1%eth0]:443"}} {
		r := ipReq("10.0.0.2:9000", map[string][]string{"X-Forwarded-For": xff})
		if got := middleware.ClientIP(r, caddy, "X-Forwarded-For"); got != netip.MustParseAddr("10.0.0.2") {
			t.Errorf("XFF %q: got %s, want the peer", xff, got)
		}
	}
}
