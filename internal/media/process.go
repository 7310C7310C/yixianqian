package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"net/http"
	"strings"

	xdraw "golang.org/x/image/draw"
)

const (
	DefaultMaxDimension = 8000
	// DefaultMaxPixels accepts 13 MP so the common iPhone 12 MP frame
	// (4032×3024 = 12,192,768 pixels) is not rejected.
	DefaultMaxPixels = 13_000_000
)

var (
	ErrUnsupportedImage = errors.New("media: only JPEG and PNG images are supported")
	ErrHEIC             = errors.New("media: HEIC/HEIF images are not supported")
	ErrImageDimensions  = errors.New("media: image dimensions exceed the limit")
	ErrFileTooLarge     = errors.New("media: file exceeds the configured size limit")
)

// Processed is a validated original plus a JPEG thumbnail.
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
	contentType := http.DetectContentType(data)
	if contentType != "image/jpeg" && contentType != "image/png" {
		return Processed{}, ErrUnsupportedImage
	}

	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Processed{}, fmt.Errorf("%w: decode config: %v", ErrUnsupportedImage, err)
	}
	if (format != "jpeg" && format != "png") || cfg.Width <= 0 || cfg.Height <= 0 {
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
	if decodedFormat == "jpeg" {
		orientation = jpegOrientation(data)
	}
	oriented := applyOrientation(src, orientation)

	original, err := encodeOriginal(oriented, decodedFormat)
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
		ContentType: contentType,
		Width:       bounds.Dx(),
		Height:      bounds.Dy(),
		Thumbnail:   thumb,
	}, nil
}

// encodeOriginal re-encodes the decoded image so the stored original carries no
// container metadata (EXIF GPS, camera, timestamps, XMP/IPTC, PNG text chunks).
func encodeOriginal(img image.Image, format string) ([]byte, error) {
	var buf bytes.Buffer
	switch format {
	case "jpeg":
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
			return nil, err
		}
	case "png":
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
	default:
		return nil, ErrUnsupportedImage
	}
	return buf.Bytes(), nil
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
