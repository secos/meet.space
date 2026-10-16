package token_test

import (
	"bytes"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"meet.space/internal/token"
)

// P0-TOKEN-05
// Rule 6: whatever string arrives in a URL or cookie, Verify does not panic,
// returns only ErrInvalid or ErrExpired on failure, and accepts (or calls
// expired) only a token Sign produced under this key. The corpus starts from
// real tokens, so the fuzzer mutates authentic input.
// OPEN (G-21): whether a token signed for one purpose may verify for another
// is not decided, so a signed token counts as authentic under any purpose
// here. Once G-21 is settled, key signed by purpose as well.
func FuzzRule6_VerifyAcceptsOnlySignedTokens(f *testing.F) {
	s := signer(f, secretA)
	other := signer(f, secretB)
	signed := map[string]bool{} // every token s signed; nothing else may verify
	seed := func(purpose string, payload []byte, exp time.Time) {
		tok := s.Sign(purpose, payload, exp)
		signed[tok] = true
		f.Add(purpose, tok)
		// The same input under another key is a forgery for s.
		f.Add(purpose, other.Sign(purpose, payload, exp))
	}
	for _, p := range []string{purposeCancel, purposeUnsubscribe, purposeRSVPCookie, "", "x\x01"} {
		seed(p, nil, time.Time{})
		seed(p, []byte("rsvp:42"), t0.Add(time.Hour))
		seed(p, []byte("rsvp:42"), t0.Add(-time.Hour)) // expired at t0
		seed(p, bytes.Repeat([]byte{0, 0xff}, 40), time.Time{})
	}
	f.Add(purposeCancel, "")
	f.Add(purposeCancel, ".")
	f.Add(purposeCancel, "AQA.AAAA")

	f.Fuzz(func(t *testing.T, purpose, tok string) {
		got, err := s.Verify(purpose, tok, t0)
		authentic := signed[tok]
		switch {
		case err == nil:
			if !authentic {
				t.Fatalf("Verify accepted %q for purpose %q; Sign never produced it", tok, purpose)
			}
		case errors.Is(err, token.ErrExpired):
			if !authentic {
				t.Fatalf("Verify called %q expired for purpose %q; only authentic tokens may read as expired", tok, purpose)
			}
			if got != nil {
				t.Fatalf("expired token returned payload %q", got)
			}
		case errors.Is(err, token.ErrInvalid):
			if got != nil {
				t.Fatalf("invalid token returned payload %q", got)
			}
		default:
			t.Fatalf("Verify returned %v, want ErrInvalid or ErrExpired", err)
		}
	})
}

// isUnreserved is RFC 3986 "unreserved": a byte that never needs escaping
// in a path segment.
func isUnreserved(c byte) bool {
	return 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' ||
		c == '-' || c == '.' || c == '_' || c == '~'
}

// isCookieOctet is RFC 6265 section 4.1.1 cookie-octet.
func isCookieOctet(c byte) bool {
	return c == 0x21 || 0x23 <= c && c <= 0x2b || 0x2d <= c && c <= 0x3a ||
		0x3c <= c && c <= 0x5b || 0x5d <= c && c <= 0x7e
}

// P0-TOKEN-04
// Tokens travel as /rsvp/cancel/{token} and /unsubscribe/{token} path
// segments (development-plan.md routes) and as the anonymous RSVP cookie
// value (Phase 2 step 3). For any purpose, payload and expiry, every Sign
// output needs no escaping in a path, is a valid cookie value, reaches a
// handler intact through http.ServeMux's {token} wildcard and through a
// Cookie header, and then verifies to the same payload.
func FuzzRule6_TokenIsPathSegmentAndCookieSafe(f *testing.F) {
	s := signer(f, secretA)
	f.Add(purposeCancel, []byte("rsvp:42"), t0.Unix(), true)
	f.Add(purposeUnsubscribe, []byte("user:1/group:2/type:reminder"), int64(0), false)
	f.Add(purposeRSVPCookie, []byte{}, int64(0), true)
	f.Add("", []byte("/../?#%;, \"\\=\x00\xff"), int64(math.MaxInt64), true)
	f.Add("p", bytes.Repeat([]byte{0xfb, 0xff}, 64), int64(math.MinInt64+1), true)

	var gotPath, gotCookie string
	mux := http.NewServeMux()
	for _, pattern := range []string{"GET /rsvp/cancel/{token}", "GET /unsubscribe/{token}"} {
		mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.PathValue("token")
			if c, err := r.Cookie("rsvp"); err == nil {
				gotCookie = c.Value
			}
		})
	}

	f.Fuzz(func(t *testing.T, purpose string, payload []byte, expUnix int64, hasExp bool) {
		var exp time.Time
		now := t0
		if hasExp {
			if expUnix == math.MinInt64 {
				t.Skip("no instant before the earliest expiry")
			}
			exp = time.Unix(expUnix, 0)
			now = time.Unix(expUnix-1, 0)
		}
		tok := s.Sign(purpose, payload, exp)

		for i := 0; i < len(tok); i++ {
			if !isUnreserved(tok[i]) {
				t.Fatalf("token %q byte %d %q needs escaping in a URL path", tok, i, tok[i])
			}
			if !isCookieOctet(tok[i]) {
				t.Fatalf("token %q byte %d %q is not an RFC 6265 cookie-octet", tok, i, tok[i])
			}
		}
		if url.PathEscape(tok) != tok {
			t.Fatalf("url.PathEscape changed %q", tok)
		}
		if tok == "." || tok == ".." {
			t.Fatalf("token %q is a dot segment", tok)
		}
		c := &http.Cookie{Name: "rsvp", Value: tok}
		if c.String() != "rsvp="+tok {
			t.Fatalf("Set-Cookie form %q altered the value %q", c.String(), tok)
		}

		for _, prefix := range []string{"/rsvp/cancel/", "/unsubscribe/"} {
			gotPath, gotCookie = "", ""
			req := httptest.NewRequest(http.MethodGet, prefix+tok, nil)
			req.AddCookie(c)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s%s: status %d", prefix, tok, rec.Code)
			}
			if gotPath != tok {
				t.Fatalf("PathValue = %q, want %q", gotPath, tok)
			}
			if gotCookie != tok {
				t.Fatalf("cookie value = %q, want %q", gotCookie, tok)
			}
		}

		got, err := s.Verify(purpose, gotPath, now)
		if err != nil {
			t.Fatalf("Verify after path round trip: %v", err)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("payload = %x, want %x", got, payload)
		}
	})
}
