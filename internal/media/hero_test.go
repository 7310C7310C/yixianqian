package media

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func TestHeroJPEGSmaller(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 512, 640))
	for y := 0; y < 640; y++ {
		for x := 0; x < 512; x++ {
			src.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	var in bytes.Buffer
	if err := jpeg.Encode(&in, src, &jpeg.Options{Quality: 82}); err != nil {
		t.Fatal(err)
	}
	out, err := HeroJPEG(in.Bytes(), 480, 72)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width > 480 || cfg.Height > 480 {
		t.Fatalf("hero size=%dx%d", cfg.Width, cfg.Height)
	}
	if len(out) >= in.Len() {
		t.Fatalf("hero bytes=%d not smaller than input=%d", len(out), in.Len())
	}
}
