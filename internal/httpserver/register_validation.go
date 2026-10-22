package httpserver

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	errIDCardFormat = errors.New("身份证号码格式不正确。")
	idCard18RE      = regexp.MustCompile(`^[0-9]{17}[0-9X]$`)
	idCard15RE      = regexp.MustCompile(`^[0-9]{15}$`)
)

// validateChineseIDCard accepts the 18-digit (checksum verified) and legacy
// 15-digit mainland formats, returning the birth date embedded in the number.
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
		return raw, birth, nil
	default:
		return "", time.Time{}, errIDCardFormat
	}
}

func idCard18ChecksumValid(id string) bool {
	weights := [17]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	codes := "10X98765432"
	sum := 0
	for i, weight := range weights {
		digit := int(id[i] - '0')
		if digit < 0 || digit > 9 {
			return false
		}
		sum += digit * weight
	}
	return id[17] == codes[sum%11]
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
