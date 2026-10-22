package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/platform"
)

const (
	CSRFCookieName = "yxq_csrf"
	CSRFFormField  = "csrf_token"
	CSRFHeader     = "X-CSRF-Token"
	csrfNonceBytes = 16
	csrfDefaultTTL = 2 * time.Hour
)

// CSRF issues and verifies HMAC-signed, expiring tokens used for
// double-submit verification against a cookie.
type CSRF struct {
	key []byte
	ttl time.Duration
	now func() time.Time
}

// NewCSRF builds a token signer. A nil/empty key disables all verification.
func NewCSRF(key []byte, ttl time.Duration) *CSRF {
	if ttl <= 0 {
		ttl = csrfDefaultTTL
	}
	copied := append([]byte(nil), key...)
	return &CSRF{key: copied, ttl: ttl, now: time.Now}
}

func newCSRFWithClock(key []byte, ttl time.Duration, now func() time.Time) *CSRF {
	c := NewCSRF(key, ttl)
	c.now = now
	return c
}

// Issue returns a fresh signed token.
func (c *CSRF) Issue() (string, error) {
	if len(c.key) == 0 {
		return "", errors.New("auth: csrf key is empty")
	}
	nonce, err := platform.RandomBytes(csrfNonceBytes)
	if err != nil {
		return "", err
	}
	payload := make([]byte, 0, csrfNonceBytes+8)
	payload = append(payload, nonce...)
	payload = binary.BigEndian.AppendUint64(payload, uint64(c.now().Add(c.ttl).Unix()))
	mac := c.sign(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac), nil
}

// Verify checks the signature and expiry. It never panics on malformed input.
func (c *CSRF) Verify(token string) bool {
	if len(c.key) == 0 {
		return false
	}
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payload) != csrfNonceBytes+8 {
		return false
	}
	mac, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	if !hmac.Equal(mac, c.sign(payload)) {
		return false
	}
	exp := int64(binary.BigEndian.Uint64(payload[csrfNonceBytes:]))
	return c.now().Unix() < exp
}

// TTL exposes the configured lifetime for cookie Max-Age.
func (c *CSRF) TTL() time.Duration { return c.ttl }

func (c *CSRF) sign(payload []byte) []byte {
	m := hmac.New(sha256.New, c.key)
	m.Write(payload)
	return m.Sum(nil)
}
