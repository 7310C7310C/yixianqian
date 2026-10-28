package media

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"

	xdraw "golang.org/x/image/draw"
)

// HeroJPEG re-encodes a JPEG thumbnail at a smaller max dimension and quality
// for the first-screen hero. The deck keeps the standard 512px thumbnail.
func HeroJPEG(data []byte, maxDim, quality int) ([]byte, error) {
	if maxDim < 32 || quality < 1 || quality > 100 {
		return nil, errors.New("media: invalid hero thumbnail options")
	}
	src, err := jpeg.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, ErrUnsupportedImage
	}
	factor := 1.0
	if w > maxDim || h > maxDim {
		fw := float64(maxDim) / float64(w)
		fh := float64(maxDim) / float64(h)
		if fh < fw {
			factor = fh
		} else {
			factor = fw
		}
	}
	dw := max(1, int(float64(w)*factor+0.5))
	dh := max(1, int(float64(h)*factor+0.5))
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
