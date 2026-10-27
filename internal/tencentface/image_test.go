package tencentface

import (
	"bytes"
	"image/jpeg"
	"testing"
)

func TestPreparePayloadDownscalesLongestEdge(t *testing.T) {
	payload, err := preparePayload(synthJPEG(t, 1200, 900), DefaultMaxEdge)
	if err != nil {
		t.Fatalf("preparePayload: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("payload is not JPEG: %v", err)
	}
	if img.Bounds().Dx() != 1080 || img.Bounds().Dy() != 810 {
		t.Fatalf("size = %dx%d, want 1080x810", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestPreparePayloadKeepsSmallImageSize(t *testing.T) {
	payload, err := preparePayload(synthJPEG(t, 320, 240), DefaultMaxEdge)
	if err != nil {
		t.Fatalf("preparePayload: %v", err)
	}
	img, err := jpeg.Decode(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("payload is not JPEG: %v", err)
	}
	if img.Bounds().Dx() != 320 || img.Bounds().Dy() != 240 {
		t.Fatalf("size = %dx%d", img.Bounds().Dx(), img.Bounds().Dy())
	}
}

func TestPreparePayloadAcceptsPNG(t *testing.T) {
	payload, err := preparePayload(synthPNG(t, 1100, 800), DefaultMaxEdge)
	if err != nil {
		t.Fatalf("preparePayload: %v", err)
	}
	if _, err := jpeg.Decode(bytes.NewReader(payload)); err != nil {
		t.Fatalf("payload is not JPEG: %v", err)
	}
}

func TestPreparePayloadRejectsGarbage(t *testing.T) {
	if _, err := preparePayload([]byte("not an image"), DefaultMaxEdge); err == nil {
		t.Fatal("garbage must be rejected")
	}
}
