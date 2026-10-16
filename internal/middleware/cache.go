package middleware

import (
	"net/http"
)

// CachePolicy is a route's declared caching class (development-plan.md,
// "Route Definitions"). The zero value is undeclared, which the route
// table refuses; at runtime everything not opted in gets NoStore.
type CachePolicy uint8

const (
	CacheUndeclared CachePolicy = iota
	// NoStore is the default for fragments, actions, private pages, errors.
	NoStore
	// PublicPage is a cacheable page: the CDN keeps it for 5 minutes.
	PublicPage
	// Immutable is for files whose URL changes when their content does.
	Immutable
)

// Header values for each policy.
const (
	NoStoreValue    = "no-store, private"
	PublicPageValue = "public, s-maxage=300"
	ImmutableValue  = "public, max-age=31536000, immutable"
)

// Value is the Cache-Control header for the policy.
func (p CachePolicy) Value() string {
	switch p {
	case PublicPage:
		return PublicPageValue
	case Immutable:
		return ImmutableValue
	default:
		return NoStoreValue
	}
}

// TokenPageReferrerPolicy is sent on every page with a token in its URL
// (decisions.md "Referrer-Policy on token pages": same-origin, so the
// token never reaches another site and the page's own POST keeps its
// Origin).
const TokenPageReferrerPolicy = "same-origin"

// PolicyWriter sets Cache-Control (and Referrer-Policy on token pages) just
// before the status line goes out, so a handler can't change them. Any
// response with a status of 400 or more is sent no-store whatever the
// route declared.
type PolicyWriter struct {
	http.ResponseWriter
	Policy    CachePolicy
	TokenPage bool
	status    int
}

// NewPolicyWriter wraps w.
func NewPolicyWriter(w http.ResponseWriter, p CachePolicy, tokenPage bool) *PolicyWriter {
	return &PolicyWriter{ResponseWriter: w, Policy: p, TokenPage: tokenPage}
}

// WriteHeader applies the policy, then writes the status.
func (pw *PolicyWriter) WriteHeader(status int) {
	if pw.status != 0 {
		return
	}
	pw.status = status
	h := pw.ResponseWriter.Header()
	policy := pw.Policy
	if status >= 400 {
		policy = NoStore
	}
	h.Set("Cache-Control", policy.Value())
	if pw.TokenPage {
		h.Set("Referrer-Policy", TokenPageReferrerPolicy)
	}
	pw.ResponseWriter.WriteHeader(status)
}

// Write sends a 200 status first if none was written.
func (pw *PolicyWriter) Write(b []byte) (int, error) {
	if pw.status == 0 {
		pw.WriteHeader(http.StatusOK)
	}
	return pw.ResponseWriter.Write(b)
}

// Status is the status written so far (0 if none).
func (pw *PolicyWriter) Status() int { return pw.status }

// Unwrap lets http.ResponseController reach the underlying writer.
func (pw *PolicyWriter) Unwrap() http.ResponseWriter { return pw.ResponseWriter }
