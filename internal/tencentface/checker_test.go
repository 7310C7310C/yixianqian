package tencentface

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestChecker(t *testing.T, tiia, iai *httptest.Server, timeout time.Duration) *Checker {
	t.Helper()
	checker, err := NewChecker(Options{
		SecretID:     "AKIDTEST000000000000000000000000",
		SecretKey:    "testsecretkey",
		Region:       "ap-guangzhou",
		TiiaEndpoint: tiia.URL,
		IaiEndpoint:  iai.URL,
		Timeout:      timeout,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("new checker: %v", err)
	}
	return checker
}

func writeTempImage(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "photo.jpg")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write temp image: %v", err)
	}
	return path
}

func TestCheckerNoCredentialsFailsOpen(t *testing.T) {
	tiia := startFake(t, &fakeAPI{t: t, service: serviceTiia, secret: "testsecretkey", body: `{"Response":{}}`})
	iai := startFake(t, &fakeAPI{t: t, service: serviceIai, secret: "testsecretkey", body: `{"Response":{}}`})
	checker, err := NewChecker(Options{TiiaEndpoint: tiia.URL, IaiEndpoint: iai.URL,
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if err != nil {
		t.Fatalf("new checker: %v", err)
	}
	if checker.Configured() {
		t.Fatal("checker without credentials must not be configured")
	}
	path := writeTempImage(t, synthJPEG(t, 400, 300))
	verdict := checker.Check(context.Background(), "m1", path)
	if verdict.Blocked || verdict.Skipped != CodeNoCredentials || verdict.Calls != 0 {
		t.Fatalf("verdict = %+v", verdict)
	}
}

func TestCheckerCostControlOnlyCallsLivenessOnLabelHit(t *testing.T) {
	tiiaFake := &fakeAPI{t: t, service: serviceTiia, secret: "testsecretkey", body: `{"Response":{"Labels":[
		{"Name":"人","Confidence":97,"SecondCategory":"人像"},
		{"Name":"树","Confidence":53,"SecondCategory":"树"}]}}`}
	iaiFake := &fakeAPI{t: t, service: serviceIai, secret: "testsecretkey", body: `{"Response":{"Score":90}}`}
	checker := newTestChecker(t, startFake(t, tiiaFake), startFake(t, iaiFake), time.Second)

	verdict := checker.Check(context.Background(), "m1", writeTempImage(t, synthJPEG(t, 400, 300)))
	if verdict.Blocked || verdict.Skipped != "" {
		t.Fatalf("verdict = %+v", verdict)
	}
	if verdict.Calls != 1 || tiiaFake.requests.Load() != 1 || iaiFake.requests.Load() != 0 {
		t.Fatalf("calls=%d tiia=%d iai=%d, want 1/1/0", verdict.Calls,
			tiiaFake.requests.Load(), iaiFake.requests.Load())
	}
}

func TestCheckerBlocksAnimalPhotoWithoutFace(t *testing.T) {
	tiiaFake := &fakeAPI{t: t, service: serviceTiia, secret: "testsecretkey", body: `{"Response":{"Labels":[
		{"Name":"宠物","Confidence":54,"SecondCategory":"宠物"},
		{"Name":"猫","Confidence":39,"SecondCategory":"宠物"}]}}`}
	iaiFake := &fakeAPI{t: t, service: serviceIai, secret: "testsecretkey",
		body: `{"Response":{"Error":{"Code":"InvalidParameterValue.NoFaceInPhoto","Message":"no face"}}}`}
	checker := newTestChecker(t, startFake(t, tiiaFake), startFake(t, iaiFake), time.Second)

	verdict := checker.Check(context.Background(), "m1", writeTempImage(t, synthJPEG(t, 400, 300)))
	if !verdict.Blocked || verdict.Reason != ReasonAnimal || verdict.Skipped != "" {
		t.Fatalf("verdict = %+v", verdict)
	}
	if verdict.Calls != 2 || tiiaFake.requests.Load() != 1 || iaiFake.requests.Load() != 1 {
		t.Fatalf("calls=%d tiia=%d iai=%d, want 2/1/1", verdict.Calls,
			tiiaFake.requests.Load(), iaiFake.requests.Load())
	}
}

func TestCheckerPassesAnimalPhotoWithHumanFace(t *testing.T) {
	tiiaFake := &fakeAPI{t: t, service: serviceTiia, secret: "testsecretkey", body: `{"Response":{"Labels":[
		{"Name":"宠物","Confidence":87,"SecondCategory":"宠物"}]}}`}
	iaiFake := &fakeAPI{t: t, service: serviceIai, secret: "testsecretkey", body: `{"Response":{"Score":88}}`}
	checker := newTestChecker(t, startFake(t, tiiaFake), startFake(t, iaiFake), time.Second)

	verdict := checker.Check(context.Background(), "m1", writeTempImage(t, synthJPEG(t, 400, 300)))
	if verdict.Blocked || verdict.Skipped != "" || verdict.Calls != 2 {
		t.Fatalf("verdict = %+v", verdict)
	}
}

func TestCheckerFailsOpenOnLivenessAuthFailure(t *testing.T) {
	tiia := startFake(t, &fakeAPI{t: t, service: serviceTiia, secret: "testsecretkey", body: `{"Response":{"Labels":[
		{"Name":"动物","Confidence":70,"SecondCategory":"野生动物"}]}}`})
	iai := startFake(t, &fakeAPI{t: t, service: serviceIai, secret: "testsecretkey",
		body: `{"Response":{"Error":{"Code":"AuthFailure.SignatureFailure","Message":"bad"}}}`})
	checker := newTestChecker(t, tiia, iai, time.Second)

	verdict := checker.Check(context.Background(), "m1", writeTempImage(t, synthJPEG(t, 400, 300)))
	if verdict.Blocked || verdict.Skipped != CodeAuthFailure {
		t.Fatalf("verdict = %+v", verdict)
	}
}

func TestCheckerFailsOpenOnLabelErrors(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"auth", 200, `{"Response":{"Error":{"Code":"AuthFailure.SecretIdNotFound"}}}`, CodeAuthFailure},
		{"server", 500, `{"Response":{}}`, CodeServerError},
		{"api", 200, `{"Response":{"Error":{"Code":"FailedOperation.InternalError"}}}`, CodeAPIError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tiiaFake := &fakeAPI{t: t, service: serviceTiia, secret: "testsecretkey",
				status: tc.status, body: tc.body}
			iaiFake := &fakeAPI{t: t, service: serviceIai, secret: "testsecretkey", body: `{"Response":{}}`}
			checker := newTestChecker(t, startFake(t, tiiaFake), startFake(t, iaiFake), time.Second)
			verdict := checker.Check(context.Background(), "m1", writeTempImage(t, synthJPEG(t, 400, 300)))
			if verdict.Blocked || verdict.Skipped != tc.want {
				t.Fatalf("verdict = %+v, want skipped %q", verdict, tc.want)
			}
			if iaiFake.requests.Load() != 0 {
				t.Fatalf("liveness must not be called after label failure")
			}
		})
	}
}

func TestCheckerFailsOpenOnTimeout(t *testing.T) {
	tiia := startFake(t, &fakeAPI{t: t, service: serviceTiia, secret: "testsecretkey",
		delay: 300 * time.Millisecond, body: `{"Response":{}}`})
	iai := startFake(t, &fakeAPI{t: t, service: serviceIai, secret: "testsecretkey", body: `{"Response":{}}`})
	checker := newTestChecker(t, tiia, iai, 50*time.Millisecond)

	started := time.Now()
	verdict := checker.Check(context.Background(), "m1", writeTempImage(t, synthJPEG(t, 400, 300)))
	if verdict.Blocked || verdict.Skipped != CodeTimeout {
		t.Fatalf("verdict = %+v", verdict)
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("check took %v", elapsed)
	}
}

func TestCheckerFailsOpenOnInvalidImage(t *testing.T) {
	tiiaFake := &fakeAPI{t: t, service: serviceTiia, secret: "testsecretkey", body: `{"Response":{}}`}
	iaiFake := &fakeAPI{t: t, service: serviceIai, secret: "testsecretkey", body: `{"Response":{}}`}
	checker := newTestChecker(t, startFake(t, tiiaFake), startFake(t, iaiFake), time.Second)

	verdict := checker.Check(context.Background(), "m1", writeTempImage(t, []byte("not an image")))
	if verdict.Blocked || verdict.Skipped != CodeInvalidImage || verdict.Calls != 0 {
		t.Fatalf("verdict = %+v", verdict)
	}
	if tiiaFake.requests.Load() != 0 {
		t.Fatal("no API call should happen for an undecodable image")
	}
}

func TestCheckerPreparesDownscaledJPEG(t *testing.T) {
	tiiaFake := &fakeAPI{t: t, service: serviceTiia, secret: "testsecretkey", body: `{"Response":{"Labels":[]}}`}
	iai := startFake(t, &fakeAPI{t: t, service: serviceIai, secret: "testsecretkey", body: `{"Response":{}}`})
	checker := newTestChecker(t, startFake(t, tiiaFake), iai, time.Second)

	verdict := checker.Check(context.Background(), "m1", writeTempImage(t, synthJPEG(t, 1200, 900)))
	if verdict.Skipped != "" {
		t.Fatalf("verdict = %+v", verdict)
	}
	raw, _ := tiiaFake.lastBody.Load().([]byte)
	var sent struct {
		ImageBase64 string `json:"ImageBase64"`
	}
	if err := json.Unmarshal(raw, &sent); err != nil {
		t.Fatalf("request body: %v", err)
	}
	payload, err := base64.StdEncoding.DecodeString(sent.ImageBase64)
	if err != nil {
		t.Fatalf("decode base64: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("payload is not JPEG: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != 1080 || bounds.Dy() != 810 {
		t.Fatalf("payload size = %dx%d, want 1080x810", bounds.Dx(), bounds.Dy())
	}
}

func TestCheckerCheckPhotoImplementsCompliance(t *testing.T) {
	tiia := startFake(t, &fakeAPI{t: t, service: serviceTiia, secret: "testsecretkey", body: `{"Response":{"Labels":[
		{"Name":"卡通","Confidence":90,"SecondCategory":"绘画卡通"}]}}`})
	iai := startFake(t, &fakeAPI{t: t, service: serviceIai, secret: "testsecretkey",
		body: `{"Response":{"Error":{"Code":"InvalidParameterValue.NoFaceInPhoto"}}}`})
	checker := newTestChecker(t, tiia, iai, time.Second)

	result := checker.CheckPhoto(context.Background(), "m1", writeTempImage(t, synthJPEG(t, 300, 300)))
	if !result.Blocked || result.Reason != ReasonCartoon {
		t.Fatalf("compliance result = %+v", result)
	}
}
