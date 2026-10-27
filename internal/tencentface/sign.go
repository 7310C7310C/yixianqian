package tencentface

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// TC3-HMAC-SHA256 签名常量。动作、版本、服务名与 v1
// web/backend/tencent_face.py 使用的 SDK 调用保持一致。
const (
	algorithm            = "TC3-HMAC-SHA256"
	jsonContentType      = "application/json; charset=utf-8"
	serviceTiia          = "tiia"
	serviceIai           = "iai"
	versionTiia          = "2019-05-29"
	versionIai           = "2020-03-03"
	actionDetectLabel    = "DetectLabel"
	actionDetectLiveFace = "DetectLiveFaceAccurate"

	// signedHeaders 必须与真正签名（而非仅发送）的头部一致。
	signedHeaders = "content-type;host;x-tc-action"
)

// signer 生成腾讯云 TC3 鉴权头。now 可注入，保证测试确定。
type signer struct {
	secretID  string
	secretKey string
	now       func() time.Time
}

// sign 返回 Authorization 与 X-TC-Timestamp。忽略返回值的签名不会泄露任何
// 凭证以外的信息；authorization 本身含 secretID，绝不能写日志。
func (s signer) sign(service, host, action string, payload []byte) (authorization, timestamp string) {
	now := s.now().UTC()
	ts := now.Unix()
	date := now.Format("2006-01-02")

	canonicalHeaders := "content-type:" + jsonContentType + "\n" +
		"host:" + host + "\n" +
		"x-tc-action:" + strings.ToLower(action) + "\n"
	// strings.Join 在已以 "\n" 结尾的 canonicalHeaders 后再补一个 "\n"，
	// 形成规范要求的空行。
	canonicalRequest := strings.Join([]string{
		http.MethodPost,
		"/",
		"",
		canonicalHeaders,
		signedHeaders,
		sha256Hex(payload),
	}, "\n")

	scope := date + "/" + service + "/tc3_request"
	stringToSign := algorithm + "\n" +
		strconv.FormatInt(ts, 10) + "\n" +
		scope + "\n" +
		sha256Hex([]byte(canonicalRequest))

	secretDate := hmacSHA256([]byte("TC3"+s.secretKey), date)
	secretService := hmacSHA256(secretDate, service)
	secretSigning := hmacSHA256(secretService, "tc3_request")
	signature := hex.EncodeToString(hmacSHA256(secretSigning, stringToSign))

	authorization = algorithm + " Credential=" + s.secretID + "/" + scope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + signature
	return authorization, strconv.FormatInt(ts, 10)
}

func sha256Hex(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, message string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(message))
	return mac.Sum(nil)
}
