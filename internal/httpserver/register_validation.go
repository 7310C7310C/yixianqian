package httpserver

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	errIDCardFormat = errors.New("身份证号码不正确。")
	idCard18RE      = regexp.MustCompile(`^[0-9]{17}[0-9X]$`)
	idCard15RE      = regexp.MustCompile(`^[0-9]{15}$`)
	wechatIDRE      = regexp.MustCompile(`^[A-Za-z0-9_.@+-]+$`)
	// mainlandMobileRE is the registration rule for a mainland China mobile
	// number. auth.NormalizePhone stays permissive for login (migrated users),
	// but new registrations must be a usable 11-digit number.
	mainlandMobileRE = regexp.MustCompile(`^1[3-9][0-9]{9}$`)
)

// validateWechatID applies the WeChat ID rules used by the registration and
// mutual-contact forms: 5-64 visible characters, ASCII letters, digits and the
// separators users commonly paste (underscore, hyphen, dot, @, plus). The rule
// is deliberately permissive enough for legacy IDs such as "wxid_xxx" while
// rejecting whitespace, control characters and markup.
func validateWechatID(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", errors.New("请填写：微信号")
	}
	if n := utf8.RuneCountInString(value); n < 5 || n > 64 {
		return "", errors.New("微信号不正确。")
	}
	if !wechatIDRE.MatchString(value) {
		return "", errors.New("微信号不正确。")
	}
	return value, nil
}

// idCardHash is a keyed digest of the normalised ID number. It lets the
// database enforce uniqueness without storing a directly brute-forceable
// digest: the key is derived from the deployment secret. The legacy 15-digit
// form is expanded to 18 digits first, so one card cannot register twice by
// alternating between the two spellings.
func idCardHash(secret, plain string) string {
	mac := hmac.New(sha256.New, idCardKey(secret))
	mac.Write([]byte(normalizeIDCardForHash(plain)))
	return hex.EncodeToString(mac.Sum(nil))
}

// validateChineseIDCard accepts the 18-digit (checksum verified) and legacy
// 15-digit mainland formats, returning the canonical 18-digit form and the
// birth date embedded in the number. The 15-digit form is never stored or
// hashed as-is: callers get the equivalent 18-digit number so ciphertext,
// last4 and uniqueness all share one spelling.
func validateChineseIDCard(raw string) (string, time.Time, error) {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	switch {
	case idCard18RE.MatchString(raw):
		birth, err := time.Parse("20060102", raw[6:14])
		if err != nil || birth.Year() < 1900 {
			return "", time.Time{}, errIDCardFormat
		}
		if !idCard18ChecksumValid(raw) {
			return "", time.Time{}, errIDCardFormat
		}
		return raw, birth, nil
	case idCard15RE.MatchString(raw):
		birth, err := time.Parse("20060102", "19"+raw[6:12])
		if err != nil || birth.Year() < 1900 {
			return "", time.Time{}, errIDCardFormat
		}
		return idCard18From15(raw), birth, nil
	default:
		return "", time.Time{}, errIDCardFormat
	}
}

// idCard18From15 expands a legacy 15-digit number to its 18-digit equivalent:
// insert the century "19" and append the ISO 7064 check digit. The caller must
// have validated the 15-digit shape.
func idCard18From15(raw string) string {
	body := raw[:6] + "19" + raw[6:15]
	return body + string(idCardCheckDigit(body))
}

// idCardCheckDigit computes the 18th character for the first 17 digits.
func idCardCheckDigit(id17 string) byte {
	weights := [17]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	codes := "10X98765432"
	sum := 0
	for i := 0; i < 17; i++ {
		sum += int(id17[i]-'0') * weights[i]
	}
	return codes[sum%11]
}

// normalizeIDCardForHash maps the legacy 15-digit form to the canonical
// 18-digit form. Anything else is upper-cased and trimmed only; validation
// rejects malformed input before it can be stored or hashed.
func normalizeIDCardForHash(raw string) string {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	if idCard15RE.MatchString(raw) {
		return idCard18From15(raw)
	}
	return raw
}

// validateBackfillIDCard is the stricter profile-backfill rule: the legacy
// 15-digit form is no longer accepted for a new submission. The embedded birth
// date is authoritative; the caller silently corrects a missing/mismatched
// profile birthday and rejects an underage ID card.
// atLeast18On reports whether the calendar date of birth is at least 18 years
// before now's calendar day. It compares date fields directly: time.Date
// normalizes Feb 29 to Mar 1 in non-leap years, which would let a 17-year-old
// through on a leap day.
func atLeast18On(birth, now time.Time) bool {
	by, bm, bd := birth.Date()
	ny, nm, nd := now.Date()
	if ny != by+18 {
		return ny > by+18
	}
	if nm != bm {
		return nm > bm
	}
	return nd >= bd
}

// validateBackfillIDCard is the stricter profile-backfill rule: the legacy
// 15-digit form is no longer accepted for a new submission. The embedded birth
// date is authoritative; the caller silently corrects a missing/mismatched
// profile birthday and rejects an underage ID card.
func validateBackfillIDCard(raw string) (string, time.Time, error) {
	raw = strings.ToUpper(strings.TrimSpace(raw))
	if !idCard18RE.MatchString(raw) {
		return "", time.Time{}, errIDCardFormat
	}
	birth, err := time.Parse("20060102", raw[6:14])
	if err != nil || birth.Year() < 1900 {
		return "", time.Time{}, errIDCardFormat
	}
	if !idCard18ChecksumValid(raw) {
		return "", time.Time{}, errIDCardFormat
	}
	return raw, birth, nil
}

func idCard18ChecksumValid(id string) bool {
	if len(id) != 18 {
		return false
	}
	for i := 0; i < 17; i++ {
		if id[i] < '0' || id[i] > '9' {
			return false
		}
	}
	return id[17] == idCardCheckDigit(id[:17])
}

func idCardKey(secret string) []byte {
	sum := sha256.Sum256([]byte("yixianqian/id-card/v1:" + secret))
	return sum[:]
}

// sealIDCard stores the identity number encrypted with AES-256-GCM. The key is
// derived from the deployment secret, so no plaintext ID ever reaches Postgres
// or logs. The nonce is prefixed to the ciphertext.
func sealIDCard(secret, plain string) ([]byte, error) {
	block, err := aes.NewCipher(idCardKey(secret))
	if err != nil {
		return nil, fmt.Errorf("register: create id cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("register: create id gcm: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("register: id nonce: %w", err)
	}
	return gcm.Seal(nonce, nonce, []byte(plain), nil), nil
}

func openIDCard(secret string, sealed []byte) (string, error) {
	block, err := aes.NewCipher(idCardKey(secret))
	if err != nil {
		return "", fmt.Errorf("register: create id cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("register: create id gcm: %w", err)
	}
	if len(sealed) < gcm.NonceSize() {
		return "", errors.New("register: id ciphertext too short")
	}
	nonce, ciphertext := sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("register: open id ciphertext: %w", err)
	}
	return string(plain), nil
}

func registrationStatusText(status string) (heading, message string) {
	switch status {
	case "pending":
		return "资料审核中", "注册资料已送出，小天使/管理员审核通过后即可使用一线牵。审核期间请勿重复提交。"
	case "rejected":
		return "审核未通过", "很抱歉，你的资料未通过审核。如有疑问请联系小天使或管理员。"
	case "left":
		return "已暂时退出", "你已暂时退出相亲市场，如需恢复请联系小天使或管理员。"
	case "active", "matched", "observer":
		return "审核已通过", "你的资料已通过审核，可以开始使用一线牵。"
	case "disabled":
		return "账号已停用", "你的账号已被停用，请联系管理员。"
	default:
		return "账号状态", "目前无法使用一线牵，请联系管理员。"
	}
}

func registrationGateCode(status string) string {
	switch status {
	case "pending":
		return "account_pending"
	case "rejected":
		return "account_rejected"
	case "left":
		return "account_left"
	default:
		return "account_gated"
	}
}
