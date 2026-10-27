package tencentface

import (
	"bytes"
	"image"
	"image/jpeg"
	"image/png"
	"testing"
)

// synthImage 生成合成测试图（渐变），不涉及任何真实用户照片。
// 直接写 Pix 而不是逐点 Set：-race 下测试仍是毫秒级。
func synthImage(width, height int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		row := img.Pix[y*img.Stride : y*img.Stride+width*4]
		g := uint8(y * 255 / max(1, height-1))
		for x := 0; x < width; x++ {
			row[x*4+0] = uint8(x * 255 / max(1, width-1))
			row[x*4+1] = g
			row[x*4+2] = 96
			row[x*4+3] = 255
		}
	}
	return img
}

func synthJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, synthImage(width, height), &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode test jpeg: %v", err)
	}
	return buf.Bytes()
}

func synthPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, synthImage(width, height)); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}
