package media

import (
	"bytes"
	"errors"
	"image"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"strconv"
	"strings"

	xdraw "golang.org/x/image/draw"
)

// ErrVariantWidth means every requested width exceeded the source image.
var ErrVariantWidth = errors.New("media: variant width exceeds source")

// ThumbVariantKey returns the object key for a width variant next to the base
// thumbnail: <name>_thumb.jpg -> <name>_thumb_768.jpg.
func ThumbVariantKey(base string, width int) string {
	if base == "" || width <= 0 {
		return base
	}
	dot := strings.LastIndex(base, ".")
	suffix := "_" + strconv.Itoa(width)
	if dot < 0 {
		return base + suffix
	}
	return base[:dot] + suffix + base[dot:]
}

// ResizeWidthsJPEG scales a JPEG/PNG once and encodes each requested width as
// JPEG. Widths larger than the source are skipped. It is used by upload and
// backfill paths so multiple variants share a single decode.
func ResizeWidthsJPEG(data []byte, widths []int, quality int) (map[int][]byte, error) {
	if quality < 1 || quality > 100 || len(widths) == 0 {
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
	out := make(map[int][]byte, len(widths))
	for _, width := range widths {
		if width < 32 || width > w {
			continue
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
		out[width] = buf.Bytes()
	}
	if len(out) == 0 {
		return nil, ErrVariantWidth
	}
	return out, nil
}

// ResizeWidthJPEG scales a JPEG/PNG to an exact pixel width (height follows
// the aspect ratio) and re-encodes it as JPEG. It is used for card srcset
// variants; callers should skip widths larger than the source to avoid fake
// upscaling.
func ResizeWidthJPEG(data []byte, width, quality int) ([]byte, error) {
	out, err := ResizeWidthsJPEG(data, []int{width}, quality)
	if err != nil {
		return nil, err
	}
	if data, ok := out[width]; ok {
		return data, nil
	}
	return nil, ErrVariantWidth
}
