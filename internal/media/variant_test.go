package media

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func TestResizeWidthJPEG(t *testing.T) {
	src := encodeTestImage(t, "jpeg", 100, 80)
	out, err := ResizeWidthJPEG(src, 50, 82)
	if err != nil {
		t.Fatal(err)
	}
	img, err := jpeg.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if got := img.Bounds().Dx(); got != 50 {
		t.Fatalf("width=%d want 50", got)
	}
	if _, err := ResizeWidthJPEG(src, 200, 82); err == nil {
		t.Fatal("expected upscale rejection")
	}
}

func TestResizeWidthsJPEGSharesDecode(t *testing.T) {
	src := encodeTestImage(t, "jpeg", 100, 80)
	out, err := ResizeWidthsJPEG(src, []int{50, 75, 200}, 82)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("variants=%d want 2 (200 must be skipped)", len(out))
	}
	for _, width := range []int{50, 75} {
		img, err := jpeg.Decode(bytes.NewReader(out[width]))
		if err != nil {
			t.Fatalf("width %d: %v", width, err)
		}
		if got := img.Bounds().Dx(); got != width {
			t.Fatalf("width=%d want %d", got, width)
		}
	}
	if _, err := ResizeWidthsJPEG(src, []int{200}, 82); err == nil {
		t.Fatal("expected upscale-only rejection")
	}
}

func BenchmarkResizeWidthJPEGThree(b *testing.B) {
	src := benchImage(b, 3000, 2000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, width := range []int{768, 1024, 1280} {
			if _, err := ResizeWidthJPEG(src, width, 82); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func BenchmarkResizeWidthsJPEGOneDecode(b *testing.B) {
	src := benchImage(b, 3000, 2000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ResizeWidthsJPEG(src, []int{768, 1024, 1280}, 82); err != nil {
			b.Fatal(err)
		}
	}
}

func benchImage(tb testing.TB, w, h int) []byte {
	tb.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 255), G: uint8(y % 255), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		tb.Fatal(err)
	}
	return buf.Bytes()
}

func BenchmarkResizeWidthsJPEGTwo(b *testing.B) {
	src := benchImage(b, 3000, 2000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := ResizeWidthsJPEG(src, []int{768, 1024}, 82); err != nil {
			b.Fatal(err)
		}
	}
}
