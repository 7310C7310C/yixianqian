package media

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png"

	xdraw "golang.org/x/image/draw"
)

// ResizeWidthJPEG scales a JPEG/PNG to an exact pixel width (height follows
// the aspect ratio) and re-encodes it as JPEG. It is used for card srcset
// variants; callers should skip widths larger than the source to avoid fake
// upscaling.
func ResizeWidthJPEG(data []byte, width, quality int) ([]byte, error) {
	if width < 32 || quality < 1 || quality > 100 {
		return nil, errors.New("media: invalid variant options")
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, ErrUnsupportedImage
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, ErrUnsupportedImage
	}
	if width > w {
		return nil, errors.New("media: variant width exceeds source")
	}
	dw := width
	dh := max(1, int(float64(h)*float64(dw)/float64(w)+0.5))
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
