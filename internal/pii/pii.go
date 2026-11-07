// Package pii owns the ID-card encryption and dedupe-hash key derivation.
//
// The encryption key and the HMAC key are derived separately so the same
// bytes are never used for both AES-GCM and HMAC-SHA256. The legacy derivation
// is kept only to read rows written before the key split; cmd/piirotate
// re-encrypts them with the new scheme.
package pii

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
)

func derive(domain, secret string) []byte {
	sum := sha256.Sum256([]byte(domain + secret))
	return sum[:]
}

func encKey(secret string) []byte  { return derive("yixianqian/id-card-enc/v1:", secret) }
func hashKey(secret string) []byte { return derive("yixianqian/id-card-hash/v1:", secret) }

// LegacyKey is the pre-split key (AES-GCM and HMAC shared it).
func LegacyKey(secret string) []byte {
	sum := sha256.Sum256([]byte("yixianqian/id-card/v1:" + secret))
	return sum[:]
}

// Seal encrypts a normalised ID card with the current encryption key.
func Seal(secret, plain string) ([]byte, error) {
	block, err := aes.NewCipher(encKey(secret))
	if err != nil {
		return nil, fmt.Errorf("pii: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("pii: create gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("pii: generate nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, []byte(plain), nil), nil
}

// Open decrypts with the current key, then falls back to the legacy key for
// rows written before the split. The second return value reports whether the
// legacy key was used (the caller may re-seal opportunistically).
func Open(secret string, sealed []byte) (string, bool, error) {
	if plain, err := openWith(encKey(secret), sealed); err == nil {
		return plain, false, nil
	}
	plain, err := openWith(LegacyKey(secret), sealed)
	if err != nil {
		return "", false, err
	}
	return plain, true, nil
}

// LegacyOpen decrypts only with the pre-split key; cmd/piirotate uses it.
func LegacyOpen(secret string, sealed []byte) (string, error) {
	return openWith(LegacyKey(secret), sealed)
}

func openWith(key, sealed []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("pii: create cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("pii: create gcm: %w", err)
	}
	if len(sealed) < gcm.NonceSize() {
		return "", errors.New("pii: ciphertext too short")
	}
	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("pii: open ciphertext: %w", err)
	}
	return string(plain), nil
}

// Hash returns the current keyed digest used for the unique index.
func Hash(secret, plain string) string {
	mac := hmac.New(sha256.New, hashKey(secret))
	mac.Write([]byte(plain))
	return hex.EncodeToString(mac.Sum(nil))
}

// LegacyHash is the pre-split digest; cmd/piirotate and the compatibility
// duplicate check use it.
func LegacyHash(secret, plain string) string {
	mac := hmac.New(sha256.New, LegacyKey(secret))
	mac.Write([]byte(plain))
	return hex.EncodeToString(mac.Sum(nil))
}
