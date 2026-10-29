package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	"strings"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

const (
	DefaultMaxDimension = 8000
	// DefaultMaxPixels is 12.5 MP so the common iPhone 12 MP frame
	// (4032×3024 = 12,192,768 pixels) is not rejected.
	DefaultMaxPixels = 12_500_000
)

var (
	ErrUnsupportedImage = errors.New("media: unsupported image format")
	ErrHEIC             = errors.New("media: HEIC/HEIF images are not supported")
	ErrImageDimensions  = errors.New("media: image dimensions exceed the limit")
	ErrFileTooLarge     = errors.New("media: file exceeds the configured size limit")
)

// Processed is a validated original plus a JPEG thumbnail. Both are JPEG:
// every supported input format is normalized to one storage/display format.
type Processed struct {
	Original    []byte
	ContentType string
	Width       int
	Height      int
	Thumbnail   []byte
}

// Process uses conservative default limits.
func Process(data []byte, thumbSize int) (Processed, error) {
	return ProcessWithLimits(data, thumbSize, DefaultMaxDimension, DefaultMaxPixels)
}

// ProcessWithLimits sniffs the container, decodes dimensions, then rewrites the
// original without metadata (EXIF/GPS/XMP/IPTC) and creates a white-backed JPEG
// thumbnail constrained to thumbSize. EXIF orientation is applied to both the
// re-encoded original and the thumbnail. The caller is expected to bound
// concurrency; decoding a 16-bit image can allocate hundreds of MiB.
func ProcessWithLimits(data []byte, thumbSize, maxDim int, maxPixels int64) (Processed, error) {
	if maxDim <= 0 || maxPixels <= 0 {
		return Processed{}, ErrImageDimensions
	}
	if len(data) == 0 {
		return Processed{}, ErrUnsupportedImage
	}
	if isHEIC(data) {
		return Processed{}, ErrHEIC
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Processed{}, fmt.Errorf("%w: decode config: %v", ErrUnsupportedImage, err)
	}
	if !supportedImageFormat(format) || cfg.Width <= 0 || cfg.Height <= 0 {
		return Processed{}, ErrUnsupportedImage
	}
	if cfg.Width > maxDim || cfg.Height > maxDim ||
		int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return Processed{}, ErrImageDimensions
	}

	src, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return Processed{}, fmt.Errorf("%w: decode image: %v", ErrUnsupportedImage, err)
	}

	orientation := 1
	switch decodedFormat {
	case "jpeg":
		orientation = jpegOrientation(data)
	case "png":
		orientation = pngOrientation(data)
	}
	oriented := applyOrientation(src, orientation)

	// One storage format for every input: JPEG. Flatten transparency onto
	// white first; Go's JPEG encoder would otherwise turn transparent pixels
	// black. GIF/WebP animations are reduced to their first frame by Decode.
	original, err := encodeJPEG(flattenWhite(oriented))
	if err != nil {
		return Processed{}, fmt.Errorf("%w: encode original: %v", ErrUnsupportedImage, err)
	}

	thumb, err := makeThumbnail(oriented, thumbSize)
	if err != nil {
		return Processed{}, err
	}
	bounds := oriented.Bounds()
	return Processed{
		Original:    original,
		ContentType: "image/jpeg",
		Width:       bounds.Dx(),
		Height:      bounds.Dy(),
		Thumbnail:   thumb,
	}, nil
}

func supportedImageFormat(format string) bool {
	switch format {
	case "jpeg", "png", "gif", "webp", "bmp", "tiff":
		return true
	default:
		return false
	}
}

// encodeJPEG re-encodes the decoded image so the stored original carries no
// container metadata (EXIF GPS, camera, timestamps, XMP/IPTC, PNG text chunks).
func encodeJPEG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 92}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// flattenWhite composites alpha onto white before JPEG encoding.
func flattenWhite(src image.Image) image.Image {
	if opaque, ok := src.(interface{ Opaque() bool }); ok && opaque.Opaque() {
		return src
	}
	b := src.Bounds()
	dst := image.NewRGBA(b)
	draw.Draw(dst, b, &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(dst, b, src, b.Min, draw.Over)
	return dst
}

func makeThumbnail(src image.Image, thumbSize int) ([]byte, error) {
	if thumbSize <= 0 {
		return nil, errors.New("media: thumbnail size must be positive")
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, ErrUnsupportedImage
	}

	factor := 1.0
	if w > thumbSize || h > thumbSize {
		fw := float64(thumbSize) / float64(w)
		fh := float64(thumbSize) / float64(h)
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
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 82}); err != nil {
		return nil, fmt.Errorf("media: encode thumbnail: %w", err)
	}
	return buf.Bytes(), nil
}

// isHEIC checks the ISO-BMFF ftyp brand list used by HEIC/HEIF files.
func isHEIC(data []byte) bool {
	if len(data) < 12 || string(data[4:8]) != "ftyp" {
		return false
	}
	heifBrands := map[string]bool{
		"heic": true, "heix": true, "hevc": true, "hevx": true,
		"heim": true, "heis": true, "hevm": true, "hevs": true,
		"mif1": true, "msf1": true,
	}
	if heifBrands[string(data[8:12])] {
		return true
	}
	size := int(binary.BigEndian.Uint32(data[0:4]))
	if size < 16 || size > len(data) {
		size = len(data)
	}
	for off := 16; off+4 <= size; off += 4 {
		if heifBrands[strings.ToLower(string(data[off:off+4]))] {
			return true
		}
	}
	return false
}
