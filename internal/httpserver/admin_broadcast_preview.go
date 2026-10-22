package httpserver

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	adminBroadcastPreviewVersion = 1
	adminBroadcastPreviewTTL     = 2 * time.Hour
)

var errAdminBroadcastPreviewInvalid = errors.New("broadcast preview token invalid")

// adminBroadcastPreviewClaims is the signed state produced by the preview
// step. The recipient fingerprint makes a same-count audience swap detectable,
// while the key binds the token to one broadcast form submission.
type adminBroadcastPreviewClaims struct {
	Version          int    `json:"v"`
	ActivityID       int64  `json:"a"`
	Key              string `json:"k"`
	IncludeSignups   bool   `json:"s"`
	IncludeObservers bool   `json:"o"`
	Count            int    `json:"c"`
	Fingerprint      string `json:"f"`
	IssuedAt         int64  `json:"t"`
}

func adminBroadcastPreviewKey(secret string) []byte {
	sum := sha256.Sum256([]byte("yixianqian/admin-broadcast-preview/v1:" + secret))
	return sum[:]
}

func signAdminBroadcastPreview(secret string, claims adminBroadcastPreviewClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("httpserver: encode broadcast preview: %w", err)
	}
	body := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, adminBroadcastPreviewKey(secret))
	_, _ = mac.Write([]byte(body))
	return body + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func verifyAdminBroadcastPreview(secret, token string, now time.Time) (adminBroadcastPreviewClaims, error) {
	token = strings.TrimSpace(token)
	parts := strings.Split(token, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return adminBroadcastPreviewClaims{}, errAdminBroadcastPreviewInvalid
	}
	provided, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return adminBroadcastPreviewClaims{}, errAdminBroadcastPreviewInvalid
	}
	mac := hmac.New(sha256.New, adminBroadcastPreviewKey(secret))
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(mac.Sum(nil), provided) {
		return adminBroadcastPreviewClaims{}, errAdminBroadcastPreviewInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return adminBroadcastPreviewClaims{}, errAdminBroadcastPreviewInvalid
	}
	var claims adminBroadcastPreviewClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return adminBroadcastPreviewClaims{}, errAdminBroadcastPreviewInvalid
	}
	if claims.Version != adminBroadcastPreviewVersion || claims.ActivityID <= 0 ||
		claims.Key == "" || claims.Count < 0 || claims.Fingerprint == "" {
		return adminBroadcastPreviewClaims{}, errAdminBroadcastPreviewInvalid
	}
	issued := time.Unix(claims.IssuedAt, 0)
	if claims.IssuedAt <= 0 || issued.After(now.Add(5*time.Minute)) ||
		now.Sub(issued) > adminBroadcastPreviewTTL {
		return adminBroadcastPreviewClaims{}, errAdminBroadcastPreviewInvalid
	}
	return claims, nil
}

// adminBroadcastFingerprint hashes the ordered recipient list. The DB audience
// queries order by user id, so the same set always yields the same string.
func adminBroadcastFingerprint(recipients []int64) string {
	h := sha256.New()
	for _, userID := range recipients {
		_, _ = fmt.Fprintf(h, "%d,", userID)
	}
	return hex.EncodeToString(h.Sum(nil))
}
