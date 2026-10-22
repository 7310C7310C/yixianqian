package platform

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// RandomBytes returns n cryptographically random bytes.
func RandomBytes(n int) ([]byte, error) {
	if n <= 0 {
		return nil, fmt.Errorf("platform: invalid random byte count %d", n)
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("platform: read random bytes: %w", err)
	}
	return b, nil
}

// RandomToken returns an unpadded base64url encoding of n random bytes.
func RandomToken(n int) (string, error) {
	b, err := RandomBytes(n)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// NewUUID returns a random RFC 4122 version 4 UUID.
func NewUUID() (string, error) {
	b, err := RandomBytes(16)
	if err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16]), nil
}

// NewRequestID returns a short opaque id for request correlation.
func NewRequestID() string {
	b, err := RandomBytes(8)
	if err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b)
}
