package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func encodeTestImage(t *testing.T, format string, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	var err error
	switch format {
	case "jpeg":
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	case "png":
		err = png.Encode(&buf, img)
	default:
		t.Fatalf("unknown format %q", format)
	}
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return buf.Bytes()
}

func TestProcessJPEG(t *testing.T) {
	data := encodeTestImage(t, "jpeg", 800, 600)
	got, err := Process(data, 256)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if got.ContentType != "image/jpeg" {
		t.Fatalf("content type = %q", got.ContentType)
	}
	if got.Width != 800 || got.Height != 600 {
		t.Fatalf("dimensions = %dx%d", got.Width, got.Height)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(got.Thumbnail))
	if err != nil {
		t.Fatalf("decode thumbnail: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("thumbnail format = %q", format)
	}
	if cfg.Width > 256 || cfg.Height > 256 {
		t.Fatalf("thumbnail too large: %dx%d", cfg.Width, cfg.Height)
	}
	if cfg.Width != 256 || cfg.Height != 192 {
		t.Fatalf("thumbnail aspect = %dx%d", cfg.Width, cfg.Height)
	}
	if !bytes.Equal(got.Original, data) {
		t.Fatal("original bytes must be preserved")
	}
}

func TestProcessPNGDoesNotUpscale(t *testing.T) {
	data := encodeTestImage(t, "png", 120, 80)
	got, err := Process(data, 256)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if got.ContentType != "image/png" {
		t.Fatalf("content type = %q", got.ContentType)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(got.Thumbnail))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 120 || cfg.Height != 80 {
		t.Fatalf("thumbnail should not upscale: %dx%d", cfg.Width, cfg.Height)
	}
}

func TestProcessHEICRejected(t *testing.T) {
	data := make([]byte, 32)
	binary.BigEndian.PutUint32(data[0:4], 32)
	copy(data[4:8], "ftyp")
	copy(data[8:12], "heic")
	if _, err := Process(data, 128); !errors.Is(err, ErrHEIC) {
		t.Fatalf("got %v, want ErrHEIC", err)
	}
}

func TestProcessUnsupported(t *testing.T) {
	if _, err := Process(nil, 128); !errors.Is(err, ErrUnsupportedImage) {
		t.Fatalf("empty: %v", err)
	}
	if _, err := Process([]byte("<html><body>hi</body></html>"), 128); !errors.Is(err, ErrUnsupportedImage) {
		t.Fatalf("html: %v", err)
	}
	corrupt := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F', 0, 0}
	if _, err := Process(corrupt, 128); !errors.Is(err, ErrUnsupportedImage) {
		t.Fatalf("corrupt jpeg: %v", err)
	}
}

func TestProcessDimensionsLimit(t *testing.T) {
	data := encodeTestImage(t, "png", 12001, 1)
	if _, err := Process(data, 128); !errors.Is(err, ErrImageDimensions) {
		t.Fatalf("got %v, want ErrImageDimensions", err)
	}
}

func TestIsHEICCompatibleBrand(t *testing.T) {
	data := make([]byte, 40)
	binary.BigEndian.PutUint32(data[0:4], 40)
	copy(data[4:8], "ftyp")
	copy(data[8:12], "mif1")
	copy(data[16:20], "heic")
	if !isHEIC(data) {
		t.Fatal("compatible brand should be detected")
	}
	if isHEIC(encodeTestImage(t, "jpeg", 10, 10)) {
		t.Fatal("jpeg must not be detected as HEIC")
	}
}
