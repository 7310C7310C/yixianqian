package media

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"time"
)

var (
	// ErrHEICUnavailable means HEIC support is disabled or the converter binary
	// is missing. Callers should keep the actionable "convert to JPEG" message.
	ErrHEICUnavailable = errors.New("media: HEIC converter unavailable")
	// ErrHEICConvert means the converter ran but failed/timed out/returned no
	// image. Do not expose the converter output or temp paths to users.
	ErrHEICConvert = errors.New("media: HEIC conversion failed")
)

// HEICOptions configures the subprocess converter. The converter is deliberately
// isolated: libheif is a C decoder and must not run in the Go process.
type HEICOptions struct {
	Enabled     bool
	ConvertPath string
	Timeout     time.Duration
	// MaxDimension/MaxPixels are checked from in-container ispe metadata
	// before the full decode, so an oversized HEIC does not consume decoder
	// memory.
	MaxDimension int
	MaxPixels    int64
	// Context is the request context; a client disconnect cancels the
	// subprocess instead of holding the global media gate until timeout.
	Context context.Context
}

const (
	// maxConvertedBytes bounds one intermediate JPEG. A 12.5MP frame is
	// normally a few MiB; multi-image HEIC can produce several files.
	maxConvertedBytes = 64 << 20
	// maxConvertedTotal bounds all files the converter writes into one temp dir.
	maxConvertedTotal = 64 << 20
	// maxConvertedImages bounds the ispe item count (primary + aux/thumbnails).
	maxConvertedImages = 16
	// 320 MiB address space stays below the 768M systemd cgroup limit while
	// leaving room for a 12.5MP HEVC decode (measured peak RSS ≈70–110 MB).
	heicMemoryLimit   = "335544320"
	heicCPULimit      = "30" // seconds
	heicFileSizeLimit = "67108864"
	heicJPEGQuality   = "95"
)

// IsHEIC reports whether data looks like an ISO-BMFF HEIC/HEIF container.
func IsHEIC(data []byte) bool { return isHEIC(data) }

type heicProbe struct {
	Width  int
	Height int
	Images int
}

// ConvertHEIC decodes a HEIC/HEIF file with heif-convert into JPEG. The caller
// then feeds that JPEG through the normal JPEG/thumbnail pipeline.
func ConvertHEIC(data []byte, opts HEICOptions) ([]byte, error) {
	if !opts.Enabled {
		return nil, ErrHEICUnavailable
	}
	if len(data) == 0 {
		return nil, ErrUnsupportedImage
	}
	convertPath, err := resolveBinary(opts.ConvertPath, "heif-convert")
	if err != nil {
		return nil, ErrHEICUnavailable
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	dir, err := os.MkdirTemp("", "yxq-heic-*")
	if err != nil {
		return nil, fmt.Errorf("%w: create temp dir: %v", ErrHEICConvert, err)
	}
	defer func() { _ = os.RemoveAll(dir) }()

	inputPath := filepath.Join(dir, "in.heic")
	if err := os.WriteFile(inputPath, data, 0o600); err != nil {
		return nil, fmt.Errorf("%w: write input: %v", ErrHEICConvert, err)
	}

	if _, err := probeHEIC(data, opts); err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(baseContext(opts.Context), timeout)
	defer cancel()
	outPath := filepath.Join(dir, "out.jpg")
	cmd := sandboxCommand(ctx, convertPath, []string{"-q", heicJPEGQuality, inputPath, outPath}, dir)
	if err := cmd.Run(); err != nil {
		return nil, ErrHEICConvert
	}
	if ctx.Err() != nil {
		return nil, ErrHEICConvert
	}
	converted, err := readConverted(dir, outPath)
	if err != nil {
		return nil, err
	}
	// Validate the converter output before handing it to the image pipeline.
	cfg, format, err := image.DecodeConfig(bytes.NewReader(converted))
	if err != nil || format != "jpeg" || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, ErrHEICConvert
	}
	if opts.MaxDimension > 0 && (cfg.Width > opts.MaxDimension || cfg.Height > opts.MaxDimension) {
		return nil, ErrImageDimensions
	}
	if opts.MaxPixels > 0 && int64(cfg.Width)*int64(cfg.Height) > opts.MaxPixels {
		return nil, ErrImageDimensions
	}
	return converted, nil
}

// probeHEIC walks the ISO-BMFF box tree (meta → iprp → ipco → ispe). It is
// pure Go metadata parsing and never decodes the image. Every ispe property
// (primary, auxiliary, thumbnails) must pass the dimension limits, so a small
// first image cannot hide a huge second image. Box parsing mirrors what libheif
// accepts, including 64-bit largesize and non-zero ispe version/flags.
func probeHEIC(data []byte, opts HEICOptions) (heicProbe, error) {
	var probe heicProbe
	top, err := readISOBoxes(data, 0, len(data))
	if err != nil {
		return probe, ErrHEICConvert
	}
	for _, box := range top {
		if box.typ != "meta" {
			continue
		}
		if box.payload+4 > box.end {
			return probe, ErrHEICConvert
		}
		metaBoxes, err := readISOBoxes(data, box.payload+4, box.end) // skip FullBox version/flags
		if err != nil {
			return probe, ErrHEICConvert
		}
		for _, metaBox := range metaBoxes {
			if metaBox.typ != "iprp" {
				continue
			}
			iprpBoxes, err := readISOBoxes(data, metaBox.payload, metaBox.end)
			if err != nil {
				return probe, ErrHEICConvert
			}
			for _, iprpBox := range iprpBoxes {
				if iprpBox.typ != "ipco" {
					continue
				}
				properties, err := readISOBoxes(data, iprpBox.payload, iprpBox.end)
				if err != nil {
					return probe, ErrHEICConvert
				}
				for _, property := range properties {
					if property.typ != "ispe" {
						continue
					}
					if property.payload+12 > property.end {
						return probe, ErrHEICConvert
					}
					width := int(binary.BigEndian.Uint32(data[property.payload+4 : property.payload+8]))
					height := int(binary.BigEndian.Uint32(data[property.payload+8 : property.payload+12]))
					if width <= 0 || height <= 0 {
						return probe, ErrHEICConvert
					}
					if probe.Images == 0 {
						probe.Width, probe.Height = width, height
					}
					probe.Images++
					if opts.MaxDimension > 0 && (width > opts.MaxDimension || height > opts.MaxDimension) {
						return probe, ErrImageDimensions
					}
					if opts.MaxPixels > 0 && int64(width)*int64(height) > opts.MaxPixels {
						return probe, ErrImageDimensions
					}
				}
			}
		}
	}
	if probe.Images == 0 {
		return probe, ErrHEICConvert
	}
	if probe.Images > maxConvertedImages {
		return probe, ErrHEICConvert
	}
	return probe, nil
}

type isoBox struct {
	typ          string
	payload, end int
}

// readISOBoxes parses sibling boxes in data[start:end], supporting 32-bit
// sizes, 64-bit largesize (size==1) and size==0 (to end of parent).
func readISOBoxes(data []byte, start, end int) ([]isoBox, error) {
	if start < 0 || end > len(data) || start > end {
		return nil, ErrHEICConvert
	}
	boxes := make([]isoBox, 0, 8)
	for offset := start; offset+8 <= end; {
		size := int(binary.BigEndian.Uint32(data[offset : offset+4]))
		typ := string(data[offset+4 : offset+8])
		header := 8
		switch size {
		case 0:
			size = end - offset
		case 1:
			if offset+16 > end {
				return nil, ErrHEICConvert
			}
			large := binary.BigEndian.Uint64(data[offset+8 : offset+16])
			if large < 16 || large > uint64(end-offset) {
				return nil, ErrHEICConvert
			}
			size = int(large)
			header = 16
		}
		if size < header || offset+size > end {
			return nil, ErrHEICConvert
		}
		boxes = append(boxes, isoBox{typ: typ, payload: offset + header, end: offset + size})
		offset += size
	}
	return boxes, nil
}

func readConverted(dir, preferred string) ([]byte, error) {
	// heif-convert writes out-1.jpg/out-2.jpg for multi-image HEIC. Take the
	// first image only, but bound the count and total bytes of every output.
	matches, err := filepath.Glob(filepath.Join(dir, "out*.jpg"))
	if err != nil {
		return nil, ErrHEICConvert
	}
	if _, err := os.Stat(preferred); err == nil {
		matches = append(matches, preferred)
	}
	sort.Strings(matches)
	files := make([]string, 0, len(matches))
	seen := make(map[string]bool, len(matches))
	var total int64
	for _, path := range matches {
		if seen[path] {
			continue
		}
		seen[path] = true
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 {
			continue
		}
		if info.Size() > maxConvertedBytes {
			return nil, ErrHEICConvert
		}
		total += info.Size()
		files = append(files, path)
	}
	if len(files) == 0 || len(files) > maxConvertedImages || total > maxConvertedTotal {
		return nil, ErrHEICConvert
	}
	if seen[preferred] {
		return readConvertedFile(preferred)
	}
	return readConvertedFile(files[0])
}

func readConvertedFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || info.Size() <= 0 || info.Size() > maxConvertedBytes {
		return nil, ErrHEICConvert
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return nil, ErrHEICConvert
	}
	return data, nil
}

func resolveBinary(configured, fallback string) (string, error) {
	name := configured
	if name == "" {
		name = fallback
	}
	if filepath.IsAbs(name) {
		info, err := os.Stat(name)
		if err != nil || info.IsDir() {
			return "", fmt.Errorf("%w: %s", ErrHEICUnavailable, name)
		}
		return name, nil
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrHEICUnavailable, name)
	}
	return path, nil
}

// sandboxCommand wraps the decoder with prlimit (memory/CPU/fd/file size) and a
// minimal environment. prlimit comes from util-linux and is present on staging
// and CI; without it only the context timeout remains.
func sandboxCommand(ctx context.Context, binary string, args []string, dir string) *exec.Cmd {
	cmdPath := binary
	cmdArgs := args
	if prlimit, err := exec.LookPath("prlimit"); err == nil {
		cmdPath = prlimit
		cmdArgs = append([]string{
			"--as=" + heicMemoryLimit,
			"--cpu=" + heicCPULimit,
			"--nofile=64",
			"--fsize=" + heicFileSizeLimit,
			"--",
			binary,
		}, args...)
	}
	cmd := exec.CommandContext(ctx, cmdPath, cmdArgs...)
	cmd.Dir = dir
	// Never inherit the service environment (secrets); the converter only needs
	// a minimal PATH/TMPDIR/LANG.
	cmd.Env = []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"TMPDIR=" + dir,
		"LANG=C",
	}
	return cmd
}

func baseContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
