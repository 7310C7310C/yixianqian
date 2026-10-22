// Package auth implements credential hashing, opaque tokens, CSRF and
// in-process rate limiting.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	MinPasswordLength = 8
	MaxPasswordLength = 128
)

var (
	ErrPasswordTooShort = errors.New("auth: password must be at least 8 characters")
	ErrPasswordTooLong  = errors.New("auth: password must be at most 128 characters")
	ErrPasswordInvalid  = errors.New("auth: password contains control characters")
	ErrPasswordHash     = errors.New("auth: malformed password hash")
)

// Params are Argon2id cost parameters. Tune only with a benchmark.
type Params struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLength  uint32
	KeyLength   uint32
}

// DefaultParams follow OWASP guidance for Argon2id (64 MiB, 3 passes).
var DefaultParams = Params{
	Memory:      64 * 1024,
	Iterations:  3,
	Parallelism: 4,
	SaltLength:  16,
	KeyLength:   32,
}

// ValidatePassword applies the product password policy.
func ValidatePassword(password string) error {
	n := utf8.RuneCountInString(password)
	if n < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if n > MaxPasswordLength {
		return ErrPasswordTooLong
	}
	for _, r := range password {
		if unicode.IsControl(r) {
			return ErrPasswordInvalid
		}
	}
	return nil
}

// TempPasswordLength is long enough for a one-time login without relying on
// copy/paste from a narrow channel.
const TempPasswordLength = 14

const tempPasswordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

// GenerateTempPassword returns a random password that satisfies the product
// policy. Ambiguous characters (0/O/1/l/I) are excluded so it can be read over
// the phone if needed.
func GenerateTempPassword() (string, error) {
	buf := make([]byte, TempPasswordLength)
	max := byte(256 - (256 % len(tempPasswordAlphabet)))
	for i := range buf {
		for {
			if _, err := rand.Read(buf[i : i+1]); err != nil {
				return "", fmt.Errorf("auth: generate temp password: %w", err)
			}
			if buf[i] < max {
				buf[i] = tempPasswordAlphabet[int(buf[i])%len(tempPasswordAlphabet)]
				break
			}
		}
	}
	return string(buf), nil
}

// HashPassword returns a PHC-formatted Argon2id hash.
func HashPassword(password string) (string, error) {
	return hashPassword(password, DefaultParams)
}

func hashPassword(password string, p Params) (string, error) {
	salt := make([]byte, p.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generate salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLength)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Iterations, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword checks a password against a PHC-formatted Argon2id hash.
func VerifyPassword(password, encoded string) (bool, error) {
	params, salt, want, err := decodeHash(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(password), salt, params.Iterations, params.Memory, params.Parallelism, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// EqualizeTiming performs the same work as a failed verification. Call it when
// the account does not exist so response timing does not reveal that fact.
func EqualizeTiming(password string) {
	salt := []byte("yixianqian-dummy")
	_ = argon2.IDKey([]byte(password), salt, DefaultParams.Iterations, DefaultParams.Memory, DefaultParams.Parallelism, DefaultParams.KeyLength)
}

func decodeHash(encoded string) (Params, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Params{}, nil, nil, ErrPasswordHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return Params{}, nil, nil, ErrPasswordHash
	}
	var p Params
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism); err != nil {
		return Params{}, nil, nil, ErrPasswordHash
	}
	if p.Memory < 8*1024 || p.Memory > 1<<20 ||
		p.Iterations < 1 || p.Iterations > 10 ||
		p.Parallelism < 1 || p.Parallelism > 32 {
		return Params{}, nil, nil, ErrPasswordHash
	}
	salt, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(parts[4], "="))
	if err != nil || len(salt) < 8 || len(salt) > 64 {
		return Params{}, nil, nil, ErrPasswordHash
	}
	key, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(parts[5], "="))
	if err != nil || len(key) < 16 || len(key) > 64 {
		return Params{}, nil, nil, ErrPasswordHash
	}
	return p, salt, key, nil
}
