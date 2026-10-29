package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"os"
	"os/exec"
	"testing"
	"time"
)

func heicTestOptions(t *testing.T) HEICOptions {
	t.Helper()
	if _, err := exec.LookPath("heif-convert"); err != nil {
		t.Skip("heif-convert not installed")
	}
	return HEICOptions{Enabled: true, ConvertPath: "heif-convert", Timeout: 20 * time.Second}
}

func TestConvertHEICToJPEG(t *testing.T) {
	opts := heicTestOptions(t)
	data, err := os.ReadFile("testdata/sample.heic")
	if err != nil {
		t.Fatal(err)
	}
	if !IsHEIC(data) {
		t.Fatal("fixture is not detected as HEIC")
	}
	out, err := ConvertHEIC(data, opts)
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil || format != "jpeg" {
		t.Fatalf("converted format = %q err=%v, want jpeg", format, err)
	}
	if cfg.Width != 64 || cfg.Height != 64 {
		t.Fatalf("converted size = %dx%d", cfg.Width, cfg.Height)
	}
}

func TestProcessHEICNormalizesToJPEG(t *testing.T) {
	opts := heicTestOptions(t)
	data, err := os.ReadFile("testdata/sample.heic")
	if err != nil {
		t.Fatal(err)
	}
	got, err := ProcessWithLimitsHEIC(data, 32, DefaultMaxDimension, DefaultMaxPixels, opts)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if got.ContentType != "image/jpeg" || got.Width != 64 || got.Height != 64 {
		t.Fatalf("processed = %+v", got)
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(got.Original))
	if err != nil || format != "jpeg" {
		t.Fatalf("original format = %q err=%v", format, err)
	}
}

// fakeHEIC builds a minimal box tree: ftyp → meta → iprp → ipco → ispe.
// version/large exercise the non-canonical ispe encodings libheif accepts.
func fakeHEIC(version byte, large bool, sizes ...[2]uint32) []byte {
	box := func(typ string, payload []byte) []byte {
		out := make([]byte, 8+len(payload))
		binary.BigEndian.PutUint32(out[0:4], uint32(len(out)))
		copy(out[4:8], typ)
		copy(out[8:], payload)
		return out
	}
	ispe := func(size [2]uint32) []byte {
		payload := make([]byte, 12)
		payload[0] = version
		binary.BigEndian.PutUint32(payload[4:8], size[0])
		binary.BigEndian.PutUint32(payload[8:12], size[1])
		if !large {
			return box("ispe", payload)
		}
		out := make([]byte, 16+len(payload))
		binary.BigEndian.PutUint32(out[0:4], 1)
		copy(out[4:8], "ispe")
		binary.BigEndian.PutUint64(out[8:16], uint64(len(out)))
		copy(out[16:], payload)
		return out
	}
	ipco := []byte{}
	for _, size := range sizes {
		ipco = append(ipco, ispe(size)...)
	}
	meta := make([]byte, 4) // FullBox version/flags
	meta = append(meta, box("iprp", box("ipco", ipco))...)
	ftyp := box("ftyp", []byte("heic\x00\x00\x00\x00mif1heic"))
	return append(ftyp, box("meta", meta)...)
}

func TestConvertHEICRejectsOversizeBeforeDecode(t *testing.T) {
	dir := t.TempDir()
	marker := dir + "/converter-called"
	converter := dir + "/converter"
	script := "#!/bin/sh\n: > " + marker + "\nexit 1\n"
	if err := os.WriteFile(converter, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	opts := HEICOptions{Enabled: true, ConvertPath: converter, Timeout: time.Second,
		MaxDimension: 8000, MaxPixels: 12_500_000}
	// 64x64 primary + 5000x5000 second image: the second item must be rejected
	// before the converter runs. Cover canonical, version!=0 and largesize ispe.
	for _, variant := range []struct {
		name    string
		version byte
		large   bool
	}{
		{"canonical", 0, false},
		{"version1", 1, false},
		{"largesize", 0, true},
	} {
		_ = os.Remove(marker)
		data := fakeHEIC(variant.version, variant.large, [2]uint32{64, 64}, [2]uint32{5000, 5000})
		if _, err := ConvertHEIC(data, opts); !errors.Is(err, ErrImageDimensions) {
			t.Fatalf("%s: oversize error = %v, want ErrImageDimensions", variant.name, err)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatalf("%s: converter ran before the dimension preflight", variant.name)
		}
	}
	// If every item passes, the converter is reached.
	if _, err := ConvertHEIC(fakeHEIC(0, false, [2]uint32{64, 64}, [2]uint32{200, 64}), opts); !errors.Is(err, ErrHEICConvert) {
		t.Fatalf("converter error = %v, want ErrHEICConvert", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("converter did not run after a passing preflight")
	}
}

func TestConvertHEICTimeout(t *testing.T) {
	dir := t.TempDir()
	slow := dir + "/slow-converter"
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nsleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	opts := HEICOptions{Enabled: true, ConvertPath: slow, Timeout: 200 * time.Millisecond}
	data := fakeHEIC(0, false, [2]uint32{64, 64})
	start := time.Now()
	if _, err := ConvertHEIC(data, opts); !errors.Is(err, ErrHEICConvert) {
		t.Fatalf("timeout error = %v, want ErrHEICConvert", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("timeout took %v", elapsed)
	}
}

func TestProcessHEICNoLongerSilentlyRejected(t *testing.T) {
	data, err := os.ReadFile("testdata/sample.heic")
	if err != nil {
		t.Fatal(err)
	}
	// The legacy API must still reject (server paths opt into ProcessWithLimitsHEIC).
	if _, err := ProcessWithLimits(data, 32, DefaultMaxDimension, DefaultMaxPixels); !errors.Is(err, ErrHEIC) {
		t.Fatalf("legacy error = %v, want ErrHEIC", err)
	}
}

func TestConvertHEICUnavailable(t *testing.T) {
	data, err := os.ReadFile("testdata/sample.heic")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ConvertHEIC(data, HEICOptions{Enabled: false}); !errors.Is(err, ErrHEICUnavailable) {
		t.Fatalf("disabled error = %v", err)
	}
	if _, err := ConvertHEIC(data, HEICOptions{Enabled: true, ConvertPath: "definitely-not-heif-convert"}); !errors.Is(err, ErrHEICUnavailable) {
		t.Fatalf("missing converter error = %v", err)
	}
}
