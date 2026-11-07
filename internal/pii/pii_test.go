package pii

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"testing"
)

func legacySeal(t *testing.T, secret, plain string) []byte {
	t.Helper()
	block, err := aes.NewCipher(LegacyKey(secret))
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	return gcm.Seal(nonce, nonce, []byte(plain), nil)
}

func TestSealOpenSeparatesKeys(t *testing.T) {
	const oldSecret = "old-session-secret-0123456789abcdef"
	const newSecret = "new-pii-secret-0123456789abcdef"
	sealed, err := Seal(newSecret, "11010519491231002X")
	if err != nil {
		t.Fatal(err)
	}
	plain, legacy, err := Open(newSecret, sealed)
	if err != nil || legacy || plain != "11010519491231002X" {
		t.Fatalf("new open: plain=%q legacy=%v err=%v", plain, legacy, err)
	}
	if _, _, err := Open(oldSecret, sealed); err == nil {
		t.Fatal("session secret must not decrypt a PII-key ciphertext")
	}
}

func TestLegacyOpenAndHashFallback(t *testing.T) {
	const oldSecret = "old-session-secret-0123456789abcdef"
	const newSecret = "new-pii-secret-0123456789abcdef"
	sealed := legacySeal(t, oldSecret, "11010519491231002X")
	plain, err := LegacyOpen(oldSecret, sealed)
	if err != nil || plain != "11010519491231002X" {
		t.Fatalf("legacy open: plain=%q err=%v", plain, err)
	}
	if _, _, err := Open(newSecret, sealed); err == nil {
		t.Fatal("a distinct PII secret must not silently decrypt legacy ciphertext")
	}
	if Hash(newSecret, plain) == LegacyHash(oldSecret, plain) {
		t.Fatal("encryption and legacy hash domains must differ")
	}
	if bytes.Equal(encKey(newSecret), hashKey(newSecret)) {
		t.Fatal("encryption and hash keys must differ")
	}
}
