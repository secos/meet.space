package middleware_test

import (
	"net/http"
	"net/http/httptest"
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
