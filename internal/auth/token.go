package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/platform"
)

// TokenBytes is the entropy of session and activation tokens.
const TokenBytes = 32

// NewToken returns a URL-safe random token and the SHA-256 hash to persist.
func NewToken() (raw string, hash string, err error) {
	raw, err = platform.RandomToken(TokenBytes)
	if err != nil {
		return "", "", fmt.Errorf("auth: generate token: %w", err)
	}
	return raw, HashToken(raw), nil
}

// HashToken is the one-way digest stored in Postgres for sessions and
// activation tokens.
func HashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

var phoneRE = regexp.MustCompile(`^\+?[0-9]{6,20}$`)

// NormalizePhone trims separators and common country-code prefixes. An
// invalid value yields an empty string.
func NormalizePhone(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", "\t", "").Replace(s)
	switch {
	case strings.HasPrefix(s, "+86"):
		s = s[3:]
	case strings.HasPrefix(s, "0086"):
		s = s[4:]
	}
	if !phoneRE.MatchString(s) {
		return ""
	}
	return s
}

// MaskPhone returns a display-safe phone value for audit logs and templates.
func MaskPhone(phone string) string {
	phone = strings.TrimSpace(phone)
	runes := []rune(phone)
	switch {
	case len(runes) >= 7:
		return string(runes[:3]) + "****" + string(runes[len(runes)-2:])
	case len(runes) >= 3:
		return string(runes[:1]) + "***"
	case len(runes) == 0:
		return ""
	default:
		return "***"
	}
}
