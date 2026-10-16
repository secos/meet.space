// Package token signs and verifies the platform's stateless tokens: the
// RSVP-cancel link, the unsubscribe link and the anonymous RSVP cookie
// (development-plan.md, Security Rule 6). Magic links and sessions are not
// these tokens; they are random values stored hashed in the database.
//
// A token is HMAC-SHA256, keyed with APP_SECRET, over a version byte, the
// purpose, the expiry (if any) and the payload. Binding the purpose means a
// token minted for one use is refused for another; covering the expiry means
// it cannot be removed or pushed later.
//
// OPEN (G-21): the documents don't say whether a token for one use must be
// refused by the others, or whether cancel and unsubscribe tokens are bound
// to a host. This code binds the purpose and not the host; no test pins
// either answer until it is decided.
//
// Wire form: base64url (no padding) of the body, a '.', then base64url of the
// MAC. Every byte is in [A-Za-z0-9_.-], so a token is one URL path segment
// that needs no escaping and is a valid RFC 6265 cookie value.
//
// Body: version (1 byte) | kind (1 byte: 0 no expiry, 1 expiry) |
// expiry as big-endian Unix seconds (8 bytes, kind 1 only) | payload.
package token

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"meet.space/internal/config"
)

// ErrInvalid means the token was not produced by Sign under this key for
// this purpose: malformed, tampered, signed with another secret, or minted
// for another purpose. ErrExpired means the token is authentic but its
// expiry has passed; it is only returned once the MAC has verified.
var (
	ErrInvalid = errors.New("token: invalid")
	ErrExpired = errors.New("token: expired")
)

const (
	version      = 1
	kindNoExpiry = 0
	kindExpiry   = 1
	headerLen    = 2 // version + kind
	expiryLen    = 8
	macLen       = sha256.Size

	// domain separates these MACs from any other use of APP_SECRET.
	domain = "signed-token/v1\x00"
)

// enc is strict so a token has exactly one spelling: non-zero trailing bits
// are refused instead of decoding to the same bytes.
var enc = base64.RawURLEncoding.Strict()

// Signer signs and verifies tokens with one secret. It is safe for
// concurrent use.
type Signer struct {
	key []byte
}

// NewSigner returns a Signer keyed with secret (APP_SECRET). It refuses a
// secret shorter than config.MinSecretLen, the same floor config enforces,
// so a secret config accepts is one the signer accepts.
func NewSigner(secret []byte) (*Signer, error) {
	if len(secret) < config.MinSecretLen {
		return nil, fmt.Errorf("token: secret must be at least %d bytes, got %d", config.MinSecretLen, len(secret))
	}
	return &Signer{key: bytes.Clone(secret)}, nil
}

// Sign returns a token for payload, usable only for purpose. A zero expires
// means the token never expires. The expiry is kept to the whole second,
// rounded down, so a token never outlives the time asked for.
func (s *Signer) Sign(purpose string, payload []byte, expires time.Time) string {
	body := make([]byte, 0, headerLen+expiryLen+len(payload))
	body = append(body, version)
	if expires.IsZero() {
		body = append(body, kindNoExpiry)
	} else {
		body = append(body, kindExpiry)
		body = binary.BigEndian.AppendUint64(body, uint64(expires.Unix()))
	}
	body = append(body, payload...)
	return enc.EncodeToString(body) + "." + enc.EncodeToString(s.mac(purpose, body))
}

// Verify returns the payload of a token that Sign produced under this key
// for purpose. Expiry is judged against now, which callers take from the
// injected clock.
//
// A token is expired from the second its expiry names onward (now >= expiry).
// OPEN: the documents don't say whether a token is still valid at the exact
// instant of its expiry; this follows RFC 7519's "exp" convention and no test
// pins it until that is decided.
func (s *Signer) Verify(purpose, token string, now time.Time) ([]byte, error) {
	if !wellFormed(token) {
		return nil, ErrInvalid
	}
	bodyPart, macPart, _ := strings.Cut(token, ".")
	body, err := enc.DecodeString(bodyPart)
	if err != nil || len(body) < headerLen {
		return nil, ErrInvalid
	}
	got, err := enc.DecodeString(macPart)
	if err != nil || len(got) != macLen {
		return nil, ErrInvalid
	}
	if !hmac.Equal(got, s.mac(purpose, body)) {
		return nil, ErrInvalid
	}

	// The body is authentic from here on.
	if body[0] != version {
		return nil, ErrInvalid
	}
	payload := body[headerLen:]
	switch body[1] {
	case kindNoExpiry:
	case kindExpiry:
		if len(payload) < expiryLen {
			return nil, ErrInvalid
		}
		expires := int64(binary.BigEndian.Uint64(payload))
		payload = payload[expiryLen:]
		// Compared as whole seconds: expires is a whole second, so
		// floor(now) >= expires exactly when now >= expires.
		if now.Unix() >= expires {
			return nil, ErrExpired
		}
	default:
		return nil, ErrInvalid
	}
	return payload, nil
}

// mac is HMAC-SHA256 over the domain, the length-prefixed purpose and the
// body. The length prefix stops a purpose and a body trading bytes.
func (s *Signer) mac(purpose string, body []byte) []byte {
	m := hmac.New(sha256.New, s.key)
	io.WriteString(m, domain)
	m.Write(binary.AppendUvarint(nil, uint64(len(purpose))))
	io.WriteString(m, purpose)
	m.Write(body)
	return m.Sum(nil)
}

// wellFormed reports whether token is two non-empty base64url runs joined by
// one '.'. Checking the alphabet first matters: Go's base64 decoder skips
// '\r' and '\n', which would let one token have many spellings.
func wellFormed(token string) bool {
	dots := 0
	for i := 0; i < len(token); i++ {
		switch c := token[i]; {
		case c == '.':
			dots++
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return dots == 1 && token[0] != '.' && token[len(token)-1] != '.'
}
