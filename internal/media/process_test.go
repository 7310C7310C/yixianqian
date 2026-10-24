package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
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
	// The original is re-encoded: bytes change and no EXIF segment survives.
	if bytes.Equal(got.Original, data) {
		t.Fatal("original bytes must be re-encoded")
	}
	if bytes.Contains(got.Original, []byte("Exif\x00\x00")) {
		t.Fatal("original must not contain an EXIF segment")
	}
	origCfg, origFormat, err := image.DecodeConfig(bytes.NewReader(got.Original))
	if err != nil || origFormat != "jpeg" {
		t.Fatalf("decode rewritten original: format=%q err=%v", origFormat, err)
	}
	if origCfg.Width != 800 || origCfg.Height != 600 {
		t.Fatalf("rewritten original dimensions = %dx%d", origCfg.Width, origCfg.Height)
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

// gridImage lays the supplied colours out row by row for exact pixel checks.
func gridImage(w, h int, colors []color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, colors[y*w+x])
		}
	}
	return img
}

// quadImage returns four solid 16x16 blocks (TL red, TR green, BL blue, BR
// white); the large blocks survive JPEG chroma subsampling, so rotations stay
// testable.
func quadImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	colors := [4]color.RGBA{
		{R: 255, A: 255}, {G: 255, A: 255},
		{B: 255, A: 255}, {R: 255, G: 255, B: 255, A: 255},
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			idx := 0
			if x >= w/2 {
				idx++
			}
			if y >= h/2 {
				idx += 2
			}
			img.Set(x, y, colors[idx])
		}
	}
	return img
}

func encodeJPEGImage(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// withEXIFOrientation inserts an APP1 EXIF segment with the given Orientation
// tag plus a GPS IFD pointer and GPS data, mimicking an iPhone photo.
func withEXIFOrientation(t *testing.T, data []byte, orientation uint16) []byte {
	t.Helper()
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xD8 {
		t.Fatalf("not a jpeg")
	}
	const tiffLen = 56
	tiff := make([]byte, tiffLen)
	copy(tiff[0:2], "II")
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)
	// IFD0: Orientation + GPS IFD pointer.
	binary.LittleEndian.PutUint16(tiff[8:10], 2)
	entry := tiff[10:]
	binary.LittleEndian.PutUint16(entry[0:2], 0x0112)
	binary.LittleEndian.PutUint16(entry[2:4], 3)
	binary.LittleEndian.PutUint32(entry[4:8], 1)
	binary.LittleEndian.PutUint16(entry[8:10], orientation)
	binary.LittleEndian.PutUint16(entry[12:14], 0x8825)
	binary.LittleEndian.PutUint16(entry[14:16], 4)
	binary.LittleEndian.PutUint32(entry[16:20], 1)
	binary.LittleEndian.PutUint32(entry[20:24], 38)
	// GPS IFD at offset 38: LatitudeRef "N".
	binary.LittleEndian.PutUint16(tiff[38:40], 1)
	binary.LittleEndian.PutUint16(tiff[40:42], 0x0001)
	binary.LittleEndian.PutUint16(tiff[42:44], 2)
	binary.LittleEndian.PutUint32(tiff[44:48], 2)
	copy(tiff[48:50], "N\x00")

	payload := append([]byte("Exif\x00\x00"), tiff...)
	segment := make([]byte, 4+len(payload))
	segment[0], segment[1] = 0xFF, 0xE1
	binary.BigEndian.PutUint16(segment[2:4], uint16(len(payload)+2))
	copy(segment[4:], payload)

	out := append([]byte{}, data[:2]...)
	out = append(out, segment...)
	out = append(out, data[2:]...)
	return out
}

func withPNGText(t *testing.T, data []byte) []byte {
	t.Helper()
	if len(data) < 33 || string(data[12:16]) != "IHDR" {
		t.Fatalf("unexpected png layout")
	}
	text := []byte("GPSLatitude\x00 25.0330,121.5654")
	chunk := make([]byte, 12+len(text))
	binary.BigEndian.PutUint32(chunk[0:4], uint32(len(text)))
	copy(chunk[4:8], "tEXt")
	copy(chunk[8:8+len(text)], text)
	binary.BigEndian.PutUint32(chunk[8+len(text):12+len(text)], crc32.ChecksumIEEE(chunk[4:8+len(text)]))
	split := 8 + 8 + int(binary.BigEndian.Uint32(data[8:12])) + 4
	out := append([]byte{}, data[:split]...)
	out = append(out, chunk...)
	out = append(out, data[split:]...)
	return out
}

func boolUnit(v bool) uint32 {
	if v {
		return 1
	}
	return 0
}

func TestProcessStripsEXIFAndAppliesOrientation(t *testing.T) {
	// 32x32 quadrants (TL red, TR green, BL blue, BR white). Orientation 6
	// (rotate 90 CW) must produce TL blue, TR red, BL white, BR green.
	data := withEXIFOrientation(t, encodeJPEGImage(t, quadImage(32, 32)), 6)

	got, err := Process(data, 256)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if bytes.Contains(got.Original, []byte("Exif\x00\x00")) {
		t.Fatal("EXIF segment survived")
	}
	if bytes.Contains(got.Original, []byte("GPS")) {
		t.Fatal("GPS metadata survived")
	}
	if got.Width != 32 || got.Height != 32 {
		t.Fatalf("oriented dimensions = %dx%d, want 32x32", got.Width, got.Height)
	}

	img, format, err := image.Decode(bytes.NewReader(got.Original))
	if err != nil || format != "jpeg" {
		t.Fatalf("decode original: format=%q err=%v", format, err)
	}
	want := [][3]uint32{
		{0, 0, 1}, // TL blue
		{1, 0, 0}, // TR red
		{1, 1, 1}, // BL white
		{0, 1, 0}, // BR green
	}
	xs := []int{8, 24, 8, 24}
	ys := []int{8, 8, 24, 24}
	for i := range want {
		r, g, b, _ := img.At(xs[i], ys[i]).RGBA()
		got := [3]uint32{boolUnit(r > 0x8000), boolUnit(g > 0x8000), boolUnit(b > 0x8000)}
		if got != want[i] {
			t.Fatalf("quadrant %d = (%d,%d,%d), want %v", i, r, g, b, want[i])
		}
	}
	thumbCfg, _, err := image.DecodeConfig(bytes.NewReader(got.Thumbnail))
	if err != nil {
		t.Fatal(err)
	}
	if thumbCfg.Width != 32 || thumbCfg.Height != 32 {
		t.Fatalf("thumbnail orientation = %dx%d, want 32x32", thumbCfg.Width, thumbCfg.Height)
	}
}

func TestProcessStripsPNGTextChunks(t *testing.T) {
	data := withPNGText(t, encodeTestImage(t, "png", 120, 80))
	got, err := Process(data, 256)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if bytes.Contains(got.Original, []byte("tEXt")) || bytes.Contains(got.Original, []byte("GPSLatitude")) {
		t.Fatal("PNG text metadata survived")
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(got.Original)); err != nil || format != "png" {
		t.Fatalf("decode rewritten png: format=%q err=%v", format, err)
	}
}

func bigJPEG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	row := img.Pix[:w*4]
	for x := 0; x < w; x++ {
		row[x*4], row[x*4+1], row[x*4+2], row[x*4+3] = 200, 150, 100, 255
	}
	for y := 1; y < h; y++ {
		copy(img.Pix[y*img.Stride:y*img.Stride+w*4], row)
	}
	return encodeJPEGImage(t, img)
}

func TestProcessAcceptsIPhone12MP(t *testing.T) {
	// 4032x3024 = 12,192,768 pixels: the common iPhone JPEG frame.
	data := bigJPEG(t, 4032, 3024)
	got, err := Process(data, 256)
	if err != nil {
		t.Fatalf("12MP jpeg rejected: %v", err)
	}
	if got.Width != 4032 || got.Height != 3024 {
		t.Fatalf("dimensions = %dx%d", got.Width, got.Height)
	}
	// The previous 12MP cap must still reject the same frame.
	if _, err := ProcessWithLimits(data, 256, DefaultMaxDimension, 12_000_000); !errors.Is(err, ErrImageDimensions) {
		t.Fatalf("over-limit pixels = %v, want ErrImageDimensions", err)
	}
}

func TestEXIFOrientationBigEndian(t *testing.T) {
	tiff := make([]byte, 26)
	copy(tiff[0:2], "MM")
	binary.BigEndian.PutUint16(tiff[2:4], 42)
	binary.BigEndian.PutUint32(tiff[4:8], 8)
	binary.BigEndian.PutUint16(tiff[8:10], 1)
	binary.BigEndian.PutUint16(tiff[10:12], 0x0112)
	binary.BigEndian.PutUint16(tiff[12:14], 3)
	binary.BigEndian.PutUint32(tiff[14:18], 1)
	binary.BigEndian.PutUint16(tiff[18:20], 8)
	if got := exifOrientation(tiff); got != 8 {
		t.Fatalf("orientation = %d, want 8", got)
	}
	if got := exifOrientation([]byte("not exif")); got != 1 {
		t.Fatalf("invalid tiff orientation = %d, want 1", got)
	}
}

func TestApplyOrientationAllCases(t *testing.T) {
	a := color.RGBA{R: 255, A: 255}
	b := color.RGBA{G: 255, A: 255}
	c := color.RGBA{B: 255, A: 255}
	d := color.RGBA{R: 255, G: 255, A: 255}
	e := color.RGBA{G: 255, B: 255, A: 255}
	f := color.RGBA{R: 255, B: 255, A: 255}
	src := gridImage(3, 2, []color.RGBA{a, b, c, d, e, f})
	cases := []struct {
		orientation int
		want        [][]color.RGBA
	}{
		{2, [][]color.RGBA{{c, b, a}, {f, e, d}}},
		{3, [][]color.RGBA{{f, e, d}, {c, b, a}}},
		{4, [][]color.RGBA{{d, e, f}, {a, b, c}}},
		{5, [][]color.RGBA{{a, d}, {b, e}, {c, f}}},
		{6, [][]color.RGBA{{d, a}, {e, b}, {f, c}}},
		{7, [][]color.RGBA{{f, c}, {e, b}, {d, a}}},
		{8, [][]color.RGBA{{c, f}, {b, e}, {a, d}}},
	}
	for _, tc := range cases {
		got := applyOrientation(src, tc.orientation)
		if got.Bounds().Dx() != len(tc.want[0]) || got.Bounds().Dy() != len(tc.want) {
			t.Fatalf("orientation %d dims = %dx%d", tc.orientation, got.Bounds().Dx(), got.Bounds().Dy())
		}
		for y := range tc.want {
			for x := range tc.want[y] {
				if rr, gg, bb, aa := got.At(x, y).RGBA(); rr != uint32(tc.want[y][x].R)*0x101 ||
					gg != uint32(tc.want[y][x].G)*0x101 || bb != uint32(tc.want[y][x].B)*0x101 || aa != 0xffff {
					t.Fatalf("orientation %d pixel (%d,%d) = (%d,%d,%d,%d), want %v",
						tc.orientation, x, y, rr, gg, bb, aa, tc.want[y][x])
				}
			}
		}
	}
}
