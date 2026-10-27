package tencentface

import (
	"strings"
	"testing"
	"time"
)

// 固定向量：由独立 Python 实现（按腾讯云 TC3 文档逐步计算）生成，用于
// 防止签名算法被“自洽但错误”地改写。凭证是测试假值。
func TestSignMatchesGoldenVector(t *testing.T) {
	payload := []byte(`{"ImageBase64":"aGVsbG8="}`)
	s := signer{
		secretID:  "AKIDEXAMPLE0000000000000000000000000000",
		secretKey: "testsecretkey000000000000000000000000",
		now:       func() time.Time { return time.Unix(1760000000, 0) },
	}
	auth, ts := s.sign(serviceTiia, "tiia.tencentcloudapi.com", actionDetectLabel, payload)
	if ts != "1760000000" {
		t.Fatalf("timestamp = %q", ts)
	}
	const want = "TC3-HMAC-SHA256 Credential=AKIDEXAMPLE0000000000000000000000000000/2025-10-09/tiia/tc3_request, SignedHeaders=content-type;host;x-tc-action, Signature=9bc957f93abc75766e49ceba04eaabb063f8cd0ff05f59620a08609390e494c7"
	if auth != want {
		t.Fatalf("authorization mismatch\n got: %s\nwant: %s", auth, want)
	}
}

func TestSignUsesUTCAndLowercasesAction(t *testing.T) {
	s := signer{
		secretID:  "id",
		secretKey: "key",
		// 2025-10-09 23:30 UTC+8 = 2025-10-09 15:30 UTC；必须按 UTC 取日期。
		now: func() time.Time { return time.Date(2025, 10, 9, 23, 30, 0, 0, time.FixedZone("CST", 8*3600)) },
	}
	auth, ts := s.sign(serviceIai, "iai.tencentcloudapi.com", "DetectLiveFaceAccurate", []byte("{}"))
	if want := "1760023800"; ts != want {
		t.Fatalf("timestamp = %q, want %q", ts, want)
	}
	if want := "/2025-10-09/iai/tc3_request"; !strings.Contains(auth, want) {
		t.Fatalf("authorization missing scope %q: %s", want, auth)
	}
}

func TestNormalizeEndpointRejectsPlainTextRemote(t *testing.T) {
	if _, err := normalizeEndpoint("http://tiia.tencentcloudapi.com", ""); err == nil {
		t.Fatal("plaintext remote endpoint must be rejected")
	}
	if _, err := normalizeEndpoint("https://tiia.tencentcloudapi.com", ""); err != nil {
		t.Fatalf("https endpoint rejected: %v", err)
	}
	if got, err := normalizeEndpoint("", defaultTiiaEndpoint); err != nil || got != defaultTiiaEndpoint {
		t.Fatalf("default endpoint = %q, %v", got, err)
	}
	if _, err := normalizeEndpoint("http://127.0.0.1:8080", ""); err != nil {
		t.Fatalf("loopback http endpoint rejected: %v", err)
	}
	if _, err := normalizeEndpoint("https://host/path", ""); err == nil {
		t.Fatal("endpoint with path must be rejected")
	}
}

func TestNormalizeVendorCode(t *testing.T) {
	cases := map[string]string{
		"InvalidParameterValue.NoFaceInPhoto": CodeNoFace,
		"AuthFailure.SignatureFailure":        CodeAuthFailure,
		"UnauthorizedOperation":               CodeAuthFailure,
		"RequestLimitExceeded":                CodeRateLimited,
		"FailedOperation.InternalError":       CodeAPIError,
		"SomethingWithSecretLookingName":      CodeAPIError,
	}
	for vendor, want := range cases {
		if got := normalizeVendorCode(vendor); got != want {
			t.Fatalf("normalizeVendorCode(%q) = %q, want %q", vendor, got, want)
		}
	}
}
