package tencentface

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// verifyTC3 从实际收到的请求重建规范请求并复核签名；实现按腾讯云文档
// 独立书写，避免直接复用被测代码。返回 error 而不是 t.Fatalf，因为它
// 运行在服务器 goroutine 里。
func verifyTC3(r *http.Request, secretKey, service string, payload []byte) error {
	auth := r.Header.Get("Authorization")
	const prefix = "TC3-HMAC-SHA256 Credential="
	if !strings.HasPrefix(auth, prefix) {
		return fmt.Errorf("authorization prefix = %q", auth)
	}
	parts := strings.Split(strings.TrimPrefix(auth, prefix), ", ")
	if len(parts) != 3 {
		return fmt.Errorf("authorization parts = %q", parts)
	}
	credential := parts[0]
	if !strings.HasPrefix(parts[1], "SignedHeaders=") || !strings.HasPrefix(parts[2], "Signature=") {
		return fmt.Errorf("authorization parts = %q", parts)
	}
	signedHeaders := strings.TrimPrefix(parts[1], "SignedHeaders=")
	signature := strings.TrimPrefix(parts[2], "Signature=")
	if signedHeaders != "content-type;host;x-tc-action" {
		return fmt.Errorf("signed headers = %q", signedHeaders)
	}

	ts := r.Header.Get("X-TC-Timestamp")
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("timestamp %q: %w", ts, err)
	}
	date := time.Unix(unix, 0).UTC().Format("2006-01-02")
	if !strings.HasSuffix(credential, "/"+date+"/"+service+"/tc3_request") {
		return fmt.Errorf("credential %q does not match date/service", credential)
	}

	canonicalHeaders := "content-type:" + r.Header.Get("Content-Type") + "\n" +
		"host:" + r.Host + "\n" +
		"x-tc-action:" + strings.ToLower(r.Header.Get("X-TC-Action")) + "\n"
	payloadHash := sha256.Sum256(payload)
	canonicalRequest := strings.Join([]string{
		http.MethodPost, "/", "", canonicalHeaders, signedHeaders, hex.EncodeToString(payloadHash[:]),
	}, "\n")
	requestHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := "TC3-HMAC-SHA256\n" + ts + "\n" + date + "/" + service + "/tc3_request\n" +
		hex.EncodeToString(requestHash[:])

	mac := func(key, msg []byte) []byte {
		h := hmac.New(sha256.New, key)
		h.Write(msg)
		return h.Sum(nil)
	}
	secretDate := mac([]byte("TC3"+secretKey), []byte(date))
	secretService := mac(secretDate, []byte(service))
	secretSigning := mac(secretService, []byte("tc3_request"))
	want := hex.EncodeToString(mac(secretSigning, []byte(stringToSign)))
	if want != signature {
		return fmt.Errorf("signature mismatch: got %s want %s", signature, want)
	}
	return nil
}

type fakeAPI struct {
	t        *testing.T
	service  string
	secret   string
	status   int
	body     string
	delay    time.Duration
	requests atomic.Int32
	lastBody atomic.Value // []byte
}

func (f *fakeAPI) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.requests.Add(1)
		if r.Method != http.MethodPost {
			f.t.Errorf("method = %s", r.Method)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			f.t.Errorf("read body: %v", err)
		}
		f.lastBody.Store(body)
		if err := verifyTC3(r, f.secret, f.service, body); err != nil {
			f.t.Errorf("signature verification: %v", err)
			http.Error(w, "signature", http.StatusBadRequest)
			return
		}
		if f.delay > 0 {
			time.Sleep(f.delay)
		}
		if f.status != 0 {
			w.WriteHeader(f.status)
		}
		_, _ = io.WriteString(w, f.body)
	}
}

func startFake(t *testing.T, f *fakeAPI) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(f.handler())
	t.Cleanup(server.Close)
	return server
}

func newTestClient(t *testing.T, tiia, iai *httptest.Server, timeout time.Duration) *Client {
	t.Helper()
	client, err := NewClient(ClientOptions{
		SecretID:     "AKIDTEST000000000000000000000000",
		SecretKey:    "testsecretkey",
		Region:       "ap-guangzhou",
		TiiaEndpoint: tiia.URL,
		IaiEndpoint:  iai.URL,
		Timeout:      timeout,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	return client
}

func TestDetectLabelParsesLabelsAndSignsRequest(t *testing.T) {
	tiiaFake := &fakeAPI{t: t, service: serviceTiia, secret: "testsecretkey",
		body: `{"Response":{"Labels":[{"Name":"宠物","Confidence":55.5,"SecondCategory":"宠物"}],"RequestId":"r"}}`}
	tiia := startFake(t, tiiaFake)
	iai := startFake(t, &fakeAPI{t: t, service: serviceIai, secret: "testsecretkey", body: `{"Response":{}}`})
	client := newTestClient(t, tiia, iai, time.Second)

	labels, err := client.DetectLabel(context.Background(), []byte("payload"))
	if err != nil {
		t.Fatalf("DetectLabel: %v", err)
	}
	if len(labels) != 1 || labels[0].Name != "宠物" || labels[0].Confidence != 55.5 ||
		labels[0].SecondCategory != "宠物" {
		t.Fatalf("labels = %+v", labels)
	}
	raw, _ := tiiaFake.lastBody.Load().([]byte)
	var sent struct {
		ImageBase64 string `json:"ImageBase64"`
	}
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if sent.ImageBase64 != "cGF5bG9hZA==" { // base64("payload")
		t.Fatalf("ImageBase64 = %q", sent.ImageBase64)
	}
}

func TestDetectLiveFaceAccurateNoFaceInPhoto(t *testing.T) {
	tiia := startFake(t, &fakeAPI{t: t, service: serviceTiia, secret: "testsecretkey", body: `{"Response":{}}`})
	iai := startFake(t, &fakeAPI{t: t, service: serviceIai, secret: "testsecretkey",
		body: `{"Response":{"Error":{"Code":"InvalidParameterValue.NoFaceInPhoto","Message":"no face"},"RequestId":"r"}}`})
	client := newTestClient(t, tiia, iai, time.Second)

	err := client.DetectLiveFaceAccurate(context.Background(), []byte("payload"))
	if !errors.Is(err, ErrNoFaceInPhoto) {
		t.Fatalf("err = %v, want ErrNoFaceInPhoto", err)
	}
	if got := err.Error(); got != "tencentface: no_face" {
		t.Fatalf("error text = %q (must be a fixed code)", got)
	}
}

func TestClientMapsErrorsToFixedCodes(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"auth failure", 200, `{"Response":{"Error":{"Code":"AuthFailure.SignatureFailure","Message":"bad sig"}}}`, CodeAuthFailure},
		{"rate limited", 200, `{"Response":{"Error":{"Code":"RequestLimitExceeded","Message":"slow down"}}}`, CodeRateLimited},
		{"vendor error", 200, `{"Response":{"Error":{"Code":"FailedOperation.InternalError","Message":"boom"}}}`, CodeAPIError},
		{"status 500", 500, `{"Response":{}}`, CodeServerError},
		{"status 429", 429, `{"Response":{}}`, CodeRateLimited},
		{"malformed", 200, `not json`, CodeInvalidResponse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tiia := startFake(t, &fakeAPI{t: t, service: serviceTiia, secret: "testsecretkey",
				status: tc.status, body: tc.body})
			iai := startFake(t, &fakeAPI{t: t, service: serviceIai, secret: "testsecretkey", body: `{"Response":{}}`})
			client := newTestClient(t, tiia, iai, time.Second)
			_, err := client.DetectLabel(context.Background(), []byte("payload"))
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if apiErr.Code != tc.want {
				t.Fatalf("code = %q, want %q", apiErr.Code, tc.want)
			}
			if strings.Contains(err.Error(), "testsecretkey") || strings.Contains(err.Error(), "AKIDTEST") {
				t.Fatalf("error leaks credentials: %v", err)
			}
			if strings.Contains(err.Error(), "bad sig") || strings.Contains(err.Error(), "boom") {
				t.Fatalf("error leaks vendor message: %v", err)
			}
		})
	}
}

func TestClientTimeoutIsFixedCodeAndDoesNotRetry(t *testing.T) {
	tiiaFake := &fakeAPI{t: t, service: serviceTiia, secret: "testsecretkey", delay: 300 * time.Millisecond,
		body: `{"Response":{}}`}
	tiia := startFake(t, tiiaFake)
	iai := startFake(t, &fakeAPI{t: t, service: serviceIai, secret: "testsecretkey", body: `{"Response":{}}`})
	client := newTestClient(t, tiia, iai, 50*time.Millisecond)

	started := time.Now()
	_, err := client.DetectLabel(context.Background(), []byte("payload"))
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != CodeTimeout {
		t.Fatalf("err = %v, want timeout", err)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("timeout took %v", elapsed)
	}
	if got := tiiaFake.requests.Load(); got != 1 {
		t.Fatalf("requests = %d, want exactly 1 (no retry)", got)
	}
}

func TestDetectLabelRejectsPlaintextRemoteEndpoint(t *testing.T) {
	if _, err := NewClient(ClientOptions{TiiaEndpoint: "http://tiia.tencentcloudapi.com"}); err == nil {
		t.Fatal("plaintext endpoint must be rejected")
	}
}
