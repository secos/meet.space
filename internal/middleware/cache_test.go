package middleware_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"meet.space/internal/middleware"
)

// Route categories (development-plan.md "Route Definitions").
func TestCachePolicy_HeaderValues(t *testing.T) {
	cases := map[middleware.CachePolicy]string{
		middleware.CacheUndeclared: "no-store, private", // fails safe
		middleware.NoStore:         "no-store, private",
		middleware.PublicPage:      "public, s-maxage=300",
		middleware.Immutable:       "public, max-age=31536000, immutable",
	}
	for p, want := range cases {
		if got := p.Value(); got != want {
			t.Errorf("policy %d: %q, want %q", p, got, want)
		}
	}
}

// P0-CACHE-02: the header is the route's declared policy and a handler
// can't change it.
func TestRule7_HandlerCannotOverrideCachePolicy(t *testing.T) {
	rec := httptest.NewRecorder()
	pw := middleware.NewPolicyWriter(rec, middleware.NoStore, false)
	pw.Header().Set("Cache-Control", "public, max-age=86400")
	pw.WriteHeader(http.StatusOK)
	if got := rec.Header().Get("Cache-Control"); got != "no-store, private" {
		t.Errorf("Cache-Control %q, want no-store, private", got)
	}
}

// P0-CACHE-06: an error response is never cacheable, even from a route that
// opted in.
func TestRule7_ErrorResponsesAreNeverCacheable(t *testing.T) {
	for _, status := range []int{400, 403, 404, 405, 500, 503} {
		for _, p := range []middleware.CachePolicy{middleware.PublicPage, middleware.Immutable} {
			rec := httptest.NewRecorder()
			middleware.NewPolicyWriter(rec, p, false).WriteHeader(status)
			if got := rec.Header().Get("Cache-Control"); got != "no-store, private" {
				t.Errorf("status %d policy %d: %q", status, p, got)
			}
		}
	}
	rec := httptest.NewRecorder()
	pw := middleware.NewPolicyWriter(rec, middleware.PublicPage, false)
	_, _ = pw.Write([]byte("ok"))
	if got := rec.Header().Get("Cache-Control"); got != "public, s-maxage=300" {
		t.Errorf("implicit 200: %q", got)
	}
}

// Token pages get Referrer-Policy: same-origin on every response
// (decisions.md "Referrer-Policy on token pages").
func TestTokenPages_SendSameOriginReferrerPolicy(t *testing.T) {
	for _, status := range []int{200, 302, 404, 500} {
		rec := httptest.NewRecorder()
		middleware.NewPolicyWriter(rec, middleware.NoStore, true).WriteHeader(status)
		if got := rec.Header().Get("Referrer-Policy"); got != "same-origin" {
			t.Errorf("status %d: Referrer-Policy %q", status, got)
		}
	}
	rec := httptest.NewRecorder()
	middleware.NewPolicyWriter(rec, middleware.NoStore, false).WriteHeader(200)
	if got := rec.Header().Get("Referrer-Policy"); got != "" {
		t.Errorf("non-token page sent Referrer-Policy %q", got)
	}
}

// Security Rule 7 (decisions.md "Cookie on a cacheable response"): a
// response the CDN could keep is sent no-store if it sets a cookie, under
// any spelling of the header, and the bug is logged as an error. The
// cookie itself still goes out.
func TestRule7_CacheableResponseThatSetsCookieIsSentNoStore(t *testing.T) {
	for _, p := range []middleware.CachePolicy{middleware.PublicPage, middleware.Immutable} {
		for _, status := range []int{200, 302} {
			for _, set := range []func(http.Header){
				func(h http.Header) { h.Set("Set-Cookie", "session=abc") },
				func(h http.Header) { h["set-cookie"] = []string{"session=abc"} },
			} {
				var logs strings.Builder
				rec := httptest.NewRecorder()
				pw := middleware.NewPolicyWriter(rec, p, false)
				pw.Logger = slog.New(slog.NewTextHandler(&logs, nil))
				set(pw.Header())
				pw.WriteHeader(status)
				if got := rec.Header().Get("Cache-Control"); got != "no-store, private" {
					t.Errorf("policy %d status %d: Cache-Control %q", p, status, got)
				}
				if len(rec.Header().Values("Set-Cookie"))+len(rec.Header()["set-cookie"]) != 1 {
					t.Errorf("policy %d status %d: cookie was not sent", p, status)
				}
				if l := logs.String(); !strings.Contains(l, "level=ERROR") || !strings.Contains(l, "cacheable response set a cookie") {
					t.Errorf("policy %d status %d: log %q", p, status, l)
				}
			}
		}
	}
}

// A no-store response that sets a cookie (a login, an RSVP) is the normal
// case: nothing is logged.
func TestRule7_NoStoreResponseMaySetCookieQuietly(t *testing.T) {
	var logs strings.Builder
	rec := httptest.NewRecorder()
	pw := middleware.NewPolicyWriter(rec, middleware.NoStore, false)
	pw.Logger = slog.New(slog.NewTextHandler(&logs, nil))
	pw.Header().Set("Set-Cookie", "session=abc")
	pw.WriteHeader(http.StatusSeeOther)
	if got := rec.Header().Get("Cache-Control"); got != "no-store, private" {
		t.Errorf("Cache-Control %q", got)
	}
	if logs.Len() != 0 {
		t.Errorf("logged %q", logs.String())
	}
}
