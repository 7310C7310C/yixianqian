package tencentface

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// 默认值与限制。
const (
	// DefaultRegion 与 v1 的 TENCENT_REGION 默认一致。
	DefaultRegion = "ap-guangzhou"
	// DefaultTimeout 覆盖一次完整判定（含两次调用）的总预算。
	DefaultTimeout = 8 * time.Second

	defaultTiiaEndpoint = "https://tiia.tencentcloudapi.com/"
	defaultIaiEndpoint  = "https://iai.tencentcloudapi.com/"

	// maxResponseBytes 限制响应体，防止异常响应占满内存。
	maxResponseBytes = 1 << 20
)

// 固定错误码。失败一律 fail-open，调用方只记录这些码。
const (
	CodeNoFace          = "no_face"
	CodeAuthFailure     = "auth_failure"
	CodeRateLimited     = "rate_limited"
	CodeTimeout         = "timeout"
	CodeNetworkError    = "network_error"
	CodeInvalidImage    = "invalid_image"
	CodeInvalidResponse = "invalid_response"
	CodeServerError     = "server_error"
	CodeAPIError        = "api_error"
)

// ErrNoFaceInPhoto 表示活体接口判定照片中没有人脸。
var ErrNoFaceInPhoto = errors.New("tencentface: " + CodeNoFace)

// APIError 是归一化后的调用错误。Code 是固定码，Vendor 是厂商原始错误码
// （仅用于测试/排查，不写日志）。
type APIError struct {
	Code   string
	Vendor string
	Status int
}

func (e *APIError) Error() string { return "tencentface: " + e.Code }

// Is 让调用方可以用 errors.Is(err, ErrNoFaceInPhoto) 判断“无人脸”。
func (e *APIError) Is(target error) bool {
	return target == ErrNoFaceInPhoto && e.Code == CodeNoFace
}

// Label 是 DetectLabel 返回的单个标签。
type Label struct {
	Name           string  `json:"Name"`
	Confidence     float64 `json:"Confidence"`
	SecondCategory string  `json:"SecondCategory"`
}

// ClientOptions 构造 Client。endpoint 仅接受 https；只有 loopback 允许
// http（供 httptest 使用），防止生产误配置成明文。
type ClientOptions struct {
	SecretID     string
	SecretKey    string
	Region       string
	TiiaEndpoint string
	IaiEndpoint  string
	Timeout      time.Duration
	HTTPClient   *http.Client
	Now          func() time.Time
}

// Client 调用腾讯云 tiia/iai 两个 action。不要把 Client 的字段写进日志。
type Client struct {
	signer  signer
	region  string
	http    *http.Client
	tiiaURL string
	iaiURL  string
}

// NewClient 构造客户端。凭证缺失不是构造错误：Checker 会按 fail-open 处理。
func NewClient(opt ClientOptions) (*Client, error) {
	tiiaURL, err := normalizeEndpoint(opt.TiiaEndpoint, defaultTiiaEndpoint)
	if err != nil {
		return nil, fmt.Errorf("tencentface: tiia endpoint: %w", err)
	}
	iaiURL, err := normalizeEndpoint(opt.IaiEndpoint, defaultIaiEndpoint)
	if err != nil {
		return nil, fmt.Errorf("tencentface: iai endpoint: %w", err)
	}
	timeout := opt.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	now := opt.Now
	if now == nil {
		now = time.Now
	}
	region := strings.TrimSpace(opt.Region)
	if region == "" {
		region = DefaultRegion
	}
	httpClient := opt.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: timeout}
	}
	return &Client{
		signer:  signer{secretID: strings.TrimSpace(opt.SecretID), secretKey: strings.TrimSpace(opt.SecretKey), now: now},
		region:  region,
		http:    httpClient,
		tiiaURL: tiiaURL,
		iaiURL:  iaiURL,
	}, nil
}

// DetectLabel 调用 tiia 2019-05-29 DetectLabel。image 是已压缩的 JPEG 字节。
func (c *Client) DetectLabel(ctx context.Context, image []byte) ([]Label, error) {
	payload, err := json.Marshal(struct {
		ImageBase64 string `json:"ImageBase64"`
	}{ImageBase64: base64.StdEncoding.EncodeToString(image)})
	if err != nil {
		return nil, &APIError{Code: CodeInvalidImage}
	}
	var out struct {
		Labels []Label `json:"Labels"`
	}
	if err := c.do(ctx, c.tiiaURL, serviceTiia, versionTiia, actionDetectLabel, payload, &out); err != nil {
		return nil, err
	}
	return out.Labels, nil
}

// DetectLiveFaceAccurate 调用 iai 2020-03-03 DetectLiveFaceAccurate。
// 返回 nil 表示检出人脸；errors.Is(err, ErrNoFaceInPhoto) 表示无人脸；
// 其余错误（超时/鉴权/限频/5xx）都应按 fail-open 处理。
func (c *Client) DetectLiveFaceAccurate(ctx context.Context, image []byte) error {
	payload, err := json.Marshal(struct {
		Image string `json:"Image"`
	}{Image: base64.StdEncoding.EncodeToString(image)})
	if err != nil {
		return &APIError{Code: CodeInvalidImage}
	}
	return c.do(ctx, c.iaiURL, serviceIai, versionIai, actionDetectLiveFace, payload, nil)
}

// responseMeta 只解析错误与 RequestId，响应体不落日志。
type responseMeta struct {
	Error *struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	} `json:"Error"`
	RequestID string `json:"RequestId"`
}

func (c *Client) do(ctx context.Context, endpoint, service, version, action string, payload []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return &APIError{Code: CodeNetworkError}
	}
	authorization, timestamp := c.signer.sign(service, req.URL.Host, action, payload)
	req.Header.Set("Content-Type", jsonContentType)
	req.Header.Set("X-TC-Action", action)
	req.Header.Set("X-TC-Version", version)
	req.Header.Set("X-TC-Timestamp", timestamp)
	if c.region != "" {
		req.Header.Set("X-TC-Region", c.region)
	}
	req.Header.Set("Authorization", authorization)

	resp, err := c.http.Do(req)
	if err != nil {
		return transportError(ctx, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return transportError(ctx, err)
	}

	var envelope struct {
		Response json.RawMessage `json:"Response"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || len(envelope.Response) == 0 {
		return statusError(resp.StatusCode)
	}
	var meta responseMeta
	if err := json.Unmarshal(envelope.Response, &meta); err != nil {
		return statusError(resp.StatusCode)
	}
	if meta.Error != nil && meta.Error.Code != "" {
		return &APIError{Code: normalizeVendorCode(meta.Error.Code), Vendor: meta.Error.Code, Status: resp.StatusCode}
	}
	if resp.StatusCode != http.StatusOK {
		return statusError(resp.StatusCode)
	}
	if out != nil {
		if err := json.Unmarshal(envelope.Response, out); err != nil {
			return &APIError{Code: CodeInvalidResponse, Status: resp.StatusCode}
		}
	}
	return nil
}

// normalizeVendorCode 把厂商错误码压成固定码，避免把任意字符串写进日志。
func normalizeVendorCode(vendor string) string {
	switch {
	case vendor == "InvalidParameterValue.NoFaceInPhoto":
		return CodeNoFace
	case strings.HasPrefix(vendor, "AuthFailure"),
		strings.HasPrefix(vendor, "UnauthorizedOperation"):
		return CodeAuthFailure
	case strings.HasPrefix(vendor, "RequestLimitExceeded"):
		return CodeRateLimited
	case vendor == "FailedOperation.ImageDecodeFailed",
		vendor == "InvalidParameterValue.ImageDecodeFailed",
		vendor == "InvalidParameter.ImageSizeTooLarge",
		vendor == "InvalidParameterValue.ImageSizeTooLarge":
		return CodeInvalidImage
	default:
		return CodeAPIError
	}
}

func statusError(status int) error {
	switch {
	case status == http.StatusTooManyRequests:
		return &APIError{Code: CodeRateLimited, Status: status}
	case status >= 500:
		return &APIError{Code: CodeServerError, Status: status}
	case status != http.StatusOK:
		return &APIError{Code: CodeAPIError, Status: status}
	default:
		return &APIError{Code: CodeInvalidResponse, Status: status}
	}
}

// transportError 不包装底层错误：net 错误可能带 URL 或代理信息。
func transportError(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &APIError{Code: CodeTimeout}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return &APIError{Code: CodeTimeout}
	}
	return &APIError{Code: CodeNetworkError}
}

// normalizeEndpoint 接受 host、https://host 或（仅 loopback）http://host。
func normalizeEndpoint(raw, def string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = def
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("invalid endpoint")
	}
	if u.Scheme != "https" {
		if u.Scheme != "http" || !isLoopback(u.Hostname()) {
			return "", errors.New("endpoint must use https")
		}
	}
	if u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("endpoint must be scheme://host[:port]")
	}
	return u.Scheme + "://" + u.Host + "/", nil
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
