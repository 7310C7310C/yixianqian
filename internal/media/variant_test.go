package media

import (
	"bytes"
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
