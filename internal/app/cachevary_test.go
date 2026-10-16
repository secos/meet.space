package app_test

import (
	"net/http"
	"testing"

	"meet.space/internal/middleware"
	"meet.space/internal/site"
	"meet.space/internal/testkit/apptest"
)

// Security Rule 7, with development-plan.md route categories 1-2, Phase 2
// (the anonymous RSVP cookie is read by "the RSVP fragment") and Phase 8
// ("The server never reads location from cookies, so pages stay
// cacheable"): a cacheable page is the same whatever cookies the request
// carries; only no-store fragments are personalized.
// TestRule7_CacheablePagesCarryNoPrivateData varies only the session
// cookie; this sends the other kinds a browser may hold. When Phase 2 names
// the anonymous RSVP cookie, use its real name here.
func TestRule7_CacheablePagesIgnoreRequestCookies(t *testing.T) {
	a := apptest.NewApp(t)
	a.Seed()
	cookies := []*http.Cookie{
		{Name: "rsvp", Value: "rule7-cookie-rsvp"},
		{Name: "lat", Value: "39.7"},
		{Name: "theme", Value: "dark"},
	}
	swept := 0
	for _, rt := range a.App.Router.Routes() {
		if rt.Cache != middleware.PublicPage {
			continue
		}
		path := samplePath(rt.Pattern)
		for host, kind := range map[string]site.Kind{apptest.PlatformHost: site.KindPlatform, apptest.GroupHost: site.KindGroup} {
			if !rt.Hosts.Has(kind) {
				continue
			}
			plain := a.Client().Get(host, path)
			if plain.StatusCode != http.StatusOK {
				t.Fatalf("%s%s: status %d", host, path, plain.StatusCode)
			}
			for _, ck := range cookies {
				r := a.Client().Get(host, path, func(req *http.Request) { req.AddCookie(ck) })
				if r.StatusCode != plain.StatusCode || r.Body != plain.Body {
					t.Errorf("%s%s differs when the request carries cookie %q", host, path, ck.Name)
				}
				if r.Header.Get("Set-Cookie") != "" {
					t.Errorf("%s%s with cookie %q sets a cookie", host, path, ck.Name)
				}
				swept++
			}
		}
	}
	if swept == 0 {
		t.Fatal("no cacheable page was checked")
	}
}
