package token_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"meet.space/internal/config"
	_ "meet.space/internal/testkit" // network guard
	"meet.space/internal/token"
)

// The three uses Security Rule 6 names. The strings are this test's own;
// the callers that land in later phases choose theirs.
const (
	purposeCancel      = "rsvp-cancel"
	purposeUnsubscribe = "unsubscribe"
	purposeRSVPCookie  = "rsvp-cookie"
)

var (
	secretA = []byte("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") // 40 bytes
	secretB = []byte("bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")

	// t0 is a fixed "now"; nothing here reads the wall clock.
	t0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
)

func signer(t testing.TB, secret []byte) *token.Signer {
	t.Helper()
	s, err := token.NewSigner(secret)
	if err != nil {
		t.Fatalf("NewSigner: %v", err)
	}
	return s
}

// split returns the decoded body and MAC of a token, relying on the wire
// form documented in token.go.
func split(t testing.TB, tok string) (body, mac []byte) {
	t.Helper()
	b, m, ok := strings.Cut(tok, ".")
	if !ok {
		t.Fatalf("token %q has no '.'", tok)
	}
	var err error
	if body, err = base64.RawURLEncoding.DecodeString(b); err != nil {
		t.Fatalf("body: %v", err)
	}
	if mac, err = base64.RawURLEncoding.DecodeString(m); err != nil {
		t.Fatalf("mac: %v", err)
	}
	return body, mac
}

func join(body, mac []byte) string {
	return base64.RawURLEncoding.EncodeToString(body) + "." + base64.RawURLEncoding.EncodeToString(mac)
}

// wantInvalid fails unless Verify refuses tok with ErrInvalid.
func wantInvalid(t *testing.T, s *token.Signer, purpose, tok string, now time.Time, what string) {
	t.Helper()
	got, err := s.Verify(purpose, tok, now)
	if !errors.Is(err, token.ErrInvalid) {
		t.Errorf("%s: Verify(%q) = %q, %v; want ErrInvalid", what, tok, got, err)
	}
}

// P0-TOKEN-01
// A token for each Rule 6 use carries its payload back unchanged, with and
// without an expiry, including empty and binary payloads.
func TestRule6_RoundTripReturnsPayload(t *testing.T) {
	s := signer(t, secretA)
	payloads := [][]byte{
		nil,
		{},
		[]byte("0192f0c4-7d1e-7a00-8000-000000000001"),
		{0x00, 0xff, '.', '/', '=', '\n'},
		bytes.Repeat([]byte{0xa5}, 1000),
	}
	for _, purpose := range []string{purposeCancel, purposeUnsubscribe, purposeRSVPCookie} {
		for _, p := range payloads {
			for _, exp := range []time.Time{{}, t0.Add(time.Hour)} {
				tok := s.Sign(purpose, p, exp)
				got, err := s.Verify(purpose, tok, t0)
				if err != nil {
					t.Fatalf("Verify(%s, exp %v) = %v", purpose, exp, err)
				}
				if !bytes.Equal(got, p) {
					t.Fatalf("payload = %x, want %x", got, p)
				}
			}
		}
	}
}

// P0-TOKEN-01
// Rule 6 names the algorithm: the MAC is HMAC-SHA256 keyed with APP_SECRET.
// The expected MAC is computed here with crypto/hmac and crypto/sha256 over
// the input token.go documents, so a token built with another hash (even
// one with a 32-byte output) or another key fails.
// OPEN (G-21): the purpose is part of the MAC input because token.go binds
// tokens to their purpose, which is not decided; if G-21 goes another way,
// macInput changes together with token.go.
func TestRule6_MACIsHMACSHA256KeyedWithAppSecret(t *testing.T) {
	s := signer(t, secretA)
	for _, c := range []struct {
		purpose string
		payload []byte
		exp     time.Time
	}{
		{purposeCancel, []byte("rsvp:42"), t0.Add(time.Hour)},
		{purposeUnsubscribe, []byte("user:1/group:2/type:reminder"), time.Time{}},
		{"", nil, time.Time{}},
	} {
		body, mac := split(t, s.Sign(c.purpose, c.payload, c.exp))
		m := hmac.New(sha256.New, secretA)
		m.Write(macInput(c.purpose, body))
		if want := m.Sum(nil); !bytes.Equal(mac, want) {
			t.Errorf("Sign(%q, %q, %v): MAC %x, want HMAC-SHA256 under APP_SECRET %x", c.purpose, c.payload, c.exp, mac, want)
		}
	}
}

// macInput is the MAC input token.go documents: a fixed label, the
// uvarint length of the purpose, the purpose, then the body.
func macInput(purpose string, body []byte) []byte {
	in := []byte("signed-token/v1\x00")
	in = binary.AppendUvarint(in, uint64(len(purpose)))
	in = append(in, purpose...)
	return append(in, body...)
}

// P0-TOKEN-02
// Workflow 10 edge case "Tampered or invalid token": changing any one byte
// of the token, to any other value, is refused as invalid. This also covers
// spellings that decode to the same bytes (non-canonical base64).
func TestRule6_EverySingleByteChangeIsRejected(t *testing.T) {
	s := signer(t, secretA)
	tokens := map[string]string{
		"no expiry":   s.Sign(purposeUnsubscribe, []byte("user:1/group:2/type:reminder"), time.Time{}),
		"expiring":    s.Sign(purposeCancel, []byte("rsvp:42"), t0.Add(time.Hour)),
		"already exp": s.Sign(purposeCancel, []byte("rsvp:42"), t0.Add(-time.Hour)),
	}
	purposeOf := map[string]string{"no expiry": purposeUnsubscribe, "expiring": purposeCancel, "already exp": purposeCancel}
	for name, tok := range tokens {
		for i := 0; i < len(tok); i++ {
			for c := 0; c < 256; c++ {
				if byte(c) == tok[i] {
					continue
				}
				mutated := tok[:i] + string([]byte{byte(c)}) + tok[i+1:]
				// A forgery of an expired token must read as invalid,
				// not expired: ErrExpired is only for authentic tokens.
				_, err := s.Verify(purposeOf[name], mutated, t0)
				if !errors.Is(err, token.ErrInvalid) {
					t.Fatalf("%s: byte %d -> %#x: Verify = %v, want ErrInvalid", name, i, c, err)
				}
			}
		}
	}
}

// P0-TOKEN-02, P0-TOKEN-03
// The MAC covers every byte of the body (version, expiry, payload) and every
// bit of the MAC matters: flipping any one bit of either, correctly
// re-encoded, is refused.
func TestRule6_EveryBitOfBodyAndMACIsCovered(t *testing.T) {
	s := signer(t, secretA)
	for _, exp := range []time.Time{{}, t0.Add(time.Hour)} {
		tok := s.Sign(purposeCancel, []byte("rsvp:42"), exp)
		body, mac := split(t, tok)
		for i := range body {
			for bit := 0; bit < 8; bit++ {
				b := bytes.Clone(body)
				b[i] ^= 1 << bit
				wantInvalid(t, s, purposeCancel, join(b, mac), t0, "body bit flip")
			}
		}
		for i := range mac {
			for bit := 0; bit < 8; bit++ {
				m := bytes.Clone(mac)
				m[i] ^= 1 << bit
				wantInvalid(t, s, purposeCancel, join(body, m), t0, "mac bit flip")
			}
		}
	}
}

// P0-TOKEN-02
// Truncated, extended, empty, garbage and re-encoded tokens are refused.
func TestRule6_MalformedTokensAreRejected(t *testing.T) {
	s := signer(t, secretA)
	tok := s.Sign(purposeCancel, []byte("rsvp:42"), t0.Add(time.Hour))
	body, mac := split(t, tok)

	t.Run("truncated", func(t *testing.T) {
		for n := 0; n < len(tok); n++ {
			wantInvalid(t, s, purposeCancel, tok[:n], t0, "prefix")
			wantInvalid(t, s, purposeCancel, tok[len(tok)-n:], t0, "suffix")
		}
	})

	t.Run("extended", func(t *testing.T) {
		// Inserting any byte anywhere is refused. '\r' and '\n' matter:
		// Go's base64 decoder skips them.
		for i := 0; i <= len(tok); i++ {
			for c := 0; c < 256; c++ {
				ext := tok[:i] + string([]byte{byte(c)}) + tok[i:]
				wantInvalid(t, s, purposeCancel, ext, t0, "insert")
			}
		}
		for _, suffix := range []string{".", ".x", "." + tok, "=", "==", "%00", " ", ";x=y"} {
			wantInvalid(t, s, purposeCancel, tok+suffix, t0, "suffix")
		}
		wantInvalid(t, s, purposeCancel, join(append(bytes.Clone(body), 0), mac), t0, "body byte appended")
		wantInvalid(t, s, purposeCancel, join(body, append(bytes.Clone(mac), 0)), t0, "mac byte appended")
		wantInvalid(t, s, purposeCancel, join(body, mac[:len(mac)-1]), t0, "mac byte dropped")
	})

	t.Run("empty and garbage", func(t *testing.T) {
		for _, g := range []string{
			"", ".", "..", "a", "a.", ".a", "a.b", "a.b.c",
			"AAAA.AAAA", "AQA.AAAA", "AQA." + strings.Repeat("A", 43),
			"AQA." + base64.RawURLEncoding.EncodeToString(make([]byte, 32)),
			strings.Repeat("A", 4096),
			"null", "undefined", "%2E", "../../admin",
			"\x00", "\xff\xfe", "é.é",
		} {
			wantInvalid(t, s, purposeCancel, g, t0, "garbage")
		}
	})

	t.Run("re-encoded", func(t *testing.T) {
		for name, e := range map[string]*base64.Encoding{
			"padded url": base64.URLEncoding,
			"std":        base64.StdEncoding,
			"raw std":    base64.RawStdEncoding,
		} {
			re := e.EncodeToString(body) + "." + e.EncodeToString(mac)
			if re == tok {
				continue // this alphabet spells the token the same way
			}
			wantInvalid(t, s, purposeCancel, re, t0, name)
		}
		wantInvalid(t, s, purposeCancel, strings.ToUpper(tok), t0, "upper")
		wantInvalid(t, s, purposeCancel, strings.Replace(tok, ".", "/", 1), t0, "slash separator")
		wantInvalid(t, s, purposeCancel, join(mac, body), t0, "parts swapped")
	})
}

// P0-TOKEN-02
// Rule 6: tokens are keyed with APP_SECRET, so a token signed under another
// secret, even one differing in a single byte, is refused.
func TestRule6_OtherSecretIsRejected(t *testing.T) {
	a := signer(t, secretA)
	b := signer(t, secretB)
	nearA := bytes.Clone(secretA)
	nearA[len(nearA)-1] ^= 1
	near := signer(t, nearA)

	for _, exp := range []time.Time{{}, t0.Add(time.Hour)} {
		tok := a.Sign(purposeUnsubscribe, []byte("email:x@example.com/group:2"), exp)
		wantInvalid(t, b, purposeUnsubscribe, tok, t0, "other secret")
		wantInvalid(t, near, purposeUnsubscribe, tok, t0, "secret one bit off")
		if _, err := a.Verify(purposeUnsubscribe, tok, t0); err != nil {
			t.Fatalf("own secret: %v", err)
		}
	}
}

// P0-TOKEN-01 (keyed by APP_SECRET)
// Rule 6: a token is only as strong as APP_SECRET. The signer refuses an
// empty secret and agrees with config on the shortest acceptable one, so the
// server cannot start with a secret config passed but the signer refuses, or
// the reverse.
// OPEN: the minimum length itself (config.MinSecretLen, 32 bytes) is not in
// the documents (see also G-09, a weak APP_SECRET in production); this test
// pins only that the two agree.
func TestRule6_SignerRefusesSecretsConfigRefuses(t *testing.T) {
	if _, err := token.NewSigner(nil); err == nil {
		t.Error("NewSigner(nil) succeeded")
	}
	if _, err := token.NewSigner([]byte{}); err == nil {
		t.Error("NewSigner(empty) succeeded")
	}
	if _, err := token.NewSigner(bytes.Repeat([]byte("k"), config.MinSecretLen-1)); err == nil {
		t.Errorf("NewSigner accepted %d bytes, below config.MinSecretLen", config.MinSecretLen-1)
	}
	if _, err := token.NewSigner(bytes.Repeat([]byte("k"), config.MinSecretLen)); err != nil {
		t.Errorf("NewSigner refused %d bytes, which config accepts: %v", config.MinSecretLen, err)
	}
}

// G-21 (no P0 id until decided)
// Rule 6 names three kinds of signed token. One minted for any of them is
// refused as any other, and by near-miss purpose strings.
// OPEN (G-21): the documents don't say whether a token for one use must be
// refused by the others (Rule 6 lists the uses; nothing addresses cross-use).
// token.go binds the purpose today, but until that is decided this test does
// not run, so it pins no answer. Remove the Skip once G-21 is settled.
func TestRule6_OtherPurposeIsRejected(t *testing.T) {
	t.Skip("OPEN G-21: purpose binding of signed tokens is not decided")
	s := signer(t, secretA)
	purposes := []string{purposeCancel, purposeUnsubscribe, purposeRSVPCookie}
	payload := []byte("0192f0c4-7d1e-7a00-8000-000000000001")
	for _, from := range purposes {
		for _, exp := range []time.Time{{}, t0.Add(time.Hour)} {
			tok := s.Sign(from, payload, exp)
			for _, as := range purposes {
				if as == from {
					continue
				}
				wantInvalid(t, s, as, tok, t0, from+" as "+as)
			}
			for _, near := range []string{"", strings.ToUpper(from), from + " ", " " + from, from + "\x00", from[:len(from)-1]} {
				wantInvalid(t, s, near, tok, t0, from+" as near-miss "+near)
			}
		}
	}
}

// P0-TOKEN-02, P0-TOKEN-03
// The MAC input frames the purpose argument and the body so neither can lend
// bytes to the other while keeping the MAC valid. Moving the first body
// byte onto the end of the purpose turns an expiring token into one with no
// expiry; moving a trailing purpose byte that equals the version byte into
// the body turns a token with no expiry into one with a forged expiry. Both
// are refused. This holds whatever G-21 decides: a signer that ignores the
// purpose also refuses them, because the body changes.
func TestRule6_PurposeCannotBorrowBodyBytes(t *testing.T) {
	s := signer(t, secretA)
	for _, exp := range []time.Time{{}, t0.Add(time.Hour)} {
		tok := s.Sign("x", []byte("payload"), exp)
		body, mac := split(t, tok)
		for k := 1; k < len(body); k++ {
			forged := join(body[k:], mac)
			wantInvalid(t, s, "x"+string(body[:k]), forged, t0, "purpose borrows body bytes")
		}
	}
	// The reverse: the purpose's last byte (0x01, the version byte) moved to
	// the front of the body. Unframed, that body reads as version 1 with an
	// expiry built from the old kind and payload bytes.
	tok := s.Sign("x\x01", []byte("payload"), time.Time{})
	body, mac := split(t, tok)
	forged := join(append([]byte{0x01}, body...), mac)
	wantInvalid(t, s, "x", forged, t0, "body borrows purpose bytes")
}

// P0-TOKEN-03
// Rule 6 "include an expiry where appropriate": a token is valid before its
// expiry and refused with ErrExpired after it.
// OPEN: the documents don't say whether a token is valid at the exact
// instant of its expiry, so that instant is not tested.
func TestRule6_ExpiryBeforeAndAfter(t *testing.T) {
	s := signer(t, secretA)
	payload := []byte("rsvp:42")
	for _, exp := range []time.Time{
		t0.Add(15 * time.Minute),
		t0.Add(15*time.Minute + 999*time.Millisecond), // not on a whole second
		t0.Add(90 * 24 * time.Hour),
	} {
		for _, before := range []time.Duration{time.Second, time.Minute, 15 * time.Minute} {
			now := exp.Add(-before)
			got, err := s.Verify(purposeCancel, s.Sign(purposeCancel, payload, exp), now)
			if err != nil || !bytes.Equal(got, payload) {
				t.Errorf("exp %v, now %v: Verify = %q, %v; want payload", exp, now, got, err)
			}
		}
		for _, after := range []time.Duration{time.Nanosecond, time.Second, 24 * time.Hour, 100 * 365 * 24 * time.Hour} {
			now := exp.Add(after)
			got, err := s.Verify(purposeCancel, s.Sign(purposeCancel, payload, exp), now)
			if !errors.Is(err, token.ErrExpired) || got != nil {
				t.Errorf("exp %v, now %v: Verify = %q, %v; want nil, ErrExpired", exp, now, got, err)
			}
		}
	}
}

// P0-TOKEN-03
// Expiry is judged against the caller's clock, not the machine's: a token
// that expired decades ago by the wall clock is valid when the injected
// clock says it is earlier, and the reverse.
func TestRule6_ExpiryJudgedAgainstCallerClock(t *testing.T) {
	s := signer(t, secretA)
	old := s.Sign(purposeCancel, []byte("p"), time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC))
	if _, err := s.Verify(purposeCancel, old, time.Date(2000, 6, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Errorf("token expiring in 2001 verified at 2000: %v", err)
	}
	future := s.Sign(purposeCancel, []byte("p"), time.Date(2999, 1, 1, 0, 0, 0, 0, time.UTC))
	if _, err := s.Verify(purposeCancel, future, time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)); !errors.Is(err, token.ErrExpired) {
		t.Errorf("token expiring in 2999 verified at 3000: %v, want ErrExpired", err)
	}
	// The same instant in another zone is the same instant.
	chicago := time.FixedZone("CDT", -5*60*60)
	tok := s.Sign(purposeCancel, []byte("p"), t0.In(chicago))
	if _, err := s.Verify(purposeCancel, tok, t0.Add(-time.Second)); err != nil {
		t.Errorf("expiry signed in America/Chicago, verified a second before in UTC: %v", err)
	}
	if _, err := s.Verify(purposeCancel, tok, t0.Add(time.Second).In(chicago)); !errors.Is(err, token.ErrExpired) {
		t.Errorf("expiry signed in America/Chicago, verified a second after: %v, want ErrExpired", err)
	}
}

// P0-TOKEN-03
// Rule 6 says "an expiry where appropriate", so some tokens carry none (a
// zero expires): those verify at any time.
func TestRule6_NoExpiryTokenValidAtAnyTime(t *testing.T) {
	s := signer(t, secretA)
	tok := s.Sign(purposeUnsubscribe, []byte("p"), time.Time{})
	for _, now := range []time.Time{
		{},
		time.Unix(0, 0),
		t0,
		t0.Add(100 * 365 * 24 * time.Hour),
		time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC),
	} {
		if got, err := s.Verify(purposeUnsubscribe, tok, now); err != nil || string(got) != "p" {
			t.Errorf("now %v: Verify = %q, %v", now, got, err)
		}
	}
}

// P0-TOKEN-03
// The MAC covers the expiry: grafting the body of a token with no expiry, or
// a later one, onto the MAC of an expiring token is refused.
func TestRule6_ExpiryCannotBeRemovedOrExtended(t *testing.T) {
	s := signer(t, secretA)
	payload := []byte("rsvp:42")
	short := s.Sign(purposeCancel, payload, t0.Add(-time.Minute)) // already expired
	_, shortMAC := split(t, short)
	for name, longer := range map[string]string{
		"no expiry":    s.Sign(purposeCancel, payload, time.Time{}),
		"later expiry": s.Sign(purposeCancel, payload, t0.Add(24*time.Hour)),
	} {
		longBody, _ := split(t, longer)
		wantInvalid(t, s, purposeCancel, join(longBody, shortMAC), t0, name)
	}
	if _, err := s.Verify(purposeCancel, short, t0); !errors.Is(err, token.ErrExpired) {
		t.Fatalf("authentic expired token: %v, want ErrExpired", err)
	}
}

// Handlers share one Signer across requests; run with -race.
func TestSignerIsSafeForConcurrentUse(t *testing.T) {
	s := signer(t, secretA)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				p := []byte{byte(g), byte(i)}
				tok := s.Sign(purposeRSVPCookie, p, t0.Add(time.Hour))
				if got, err := s.Verify(purposeRSVPCookie, tok, t0); err != nil || !bytes.Equal(got, p) {
					t.Errorf("goroutine %d: %q, %v", g, got, err)
					return
				}
			}
		}()
	}
	wg.Wait()
}
