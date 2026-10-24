package media

import (
	"encoding/binary"
	"image"
	"image/draw"
)

// jpegOrientation reads the EXIF Orientation tag from a JPEG by walking its
// marker segments. It returns 1 (no transform) when EXIF is absent or the
// payload cannot be parsed: uploads must never fail because of metadata.
func jpegOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(data); {
		if data[i] != 0xFF {
			return 1
		}
		marker := data[i+1]
		// Standalone markers carry no length field.
		if marker == 0xFF {
			i++
			continue
		}
		if marker == 0x01 || (marker >= 0xD0 && marker <= 0xD9) {
			i += 2
			continue
		}
		// Start of scan or end of image: no more metadata segments.
		if marker == 0xDA || marker == 0xD9 {
			return 1
		}
		size := int(binary.BigEndian.Uint16(data[i+2 : i+4]))
		if size < 2 || i+2+size > len(data) {
			return 1
		}
		payload := data[i+4 : i+2+size]
		if marker == 0xE1 && len(payload) >= 6 && string(payload[:6]) == "Exif\x00\x00" {
			return exifOrientation(payload[6:])
		}
		i += 2 + size
	}
	return 1
}

// pngOrientation reads the EXIF Orientation tag from a PNG eXIf chunk. The
// chunk payload is a TIFF header without the JPEG "Exif\0\0" prefix. Chunks
// are scanned until IDAT; malformed data returns 1 (no transform) so uploads
// never fail because of metadata.
func pngOrientation(data []byte) int {
	const signature = "\x89PNG\r\n\x1a\n"
	if len(data) < 8 || string(data[:8]) != signature {
		return 1
	}
	for off := 8; off+8 <= len(data); {
		length := int(binary.BigEndian.Uint32(data[off : off+4]))
		typ := string(data[off+4 : off+8])
		if off+12+length > len(data) {
			return 1
		}
		if typ == "eXIf" {
			return exifOrientation(data[off+8 : off+8+length])
		}
		if typ == "IDAT" || typ == "IEND" {
			return 1
		}
		off += 12 + length
	}
	return 1
}

// exifOrientation parses the Orientation entry from a TIFF header as embedded
// in an APP1 segment (II/MM byte order supported).
func exifOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 1
	}
	ifd := int(order.Uint32(tiff[4:8]))
	if ifd < 8 || ifd+2 > len(tiff) {
		return 1
	}
	entries := int(order.Uint16(tiff[ifd : ifd+2]))
	pos := ifd + 2
	for i := 0; i < entries; i++ {
		if pos+12 > len(tiff) {
			return 1
		}
		tag := order.Uint16(tiff[pos : pos+2])
		typ := order.Uint16(tiff[pos+2 : pos+4])
		count := int(order.Uint32(tiff[pos+4 : pos+8]))
		if tag == 0x0112 && typ == 3 && count >= 1 {
			v := int(order.Uint16(tiff[pos+8 : pos+10]))
			if v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
		pos += 12
	}
	return 1
}

// applyOrientation returns img transformed so that pixels are upright for the
// given EXIF orientation. Orientation 1 (and anything unknown) returns the
// original image without copying.
func applyOrientation(img image.Image, orientation int) image.Image {
	if orientation < 2 || orientation > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return img
	}
	dw, dh := w, h
	if orientation >= 5 {
		dw, dh = h, w
	}
	src := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(src, src.Bounds(), img, b.Min, draw.Src)
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for dy := 0; dy < dh; dy++ {
		for dx := 0; dx < dw; dx++ {
			var sx, sy int
			switch orientation {
			case 2: // mirror horizontal
				sx, sy = w-1-dx, dy
			case 3: // rotate 180
				sx, sy = w-1-dx, h-1-dy
			case 4: // mirror vertical
				sx, sy = dx, h-1-dy
			case 5: // transpose
				sx, sy = dy, dx
			case 6: // rotate 90 CW
				sx, sy = dy, h-1-dx
			case 7: // transverse
				sx, sy = w-1-dy, h-1-dx
			case 8: // rotate 270 CW
				sx, sy = w-1-dy, dx
			}
			si := src.PixOffset(sx, sy)
			di := dst.PixOffset(dx, dy)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}
