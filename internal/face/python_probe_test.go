package face

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeProbeScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "probe.sh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPythonProbeParsesResult(t *testing.T) {
	script := writeProbeScript(t, `printf '%s\n' '{"has_face":true,"center":[0.5,0.4],"box":[0.2,0.3],"width":800,"height":600}'`)
	probe, err := NewPythonProbe(script, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	result, err := probe.Probe(context.Background(), "/tmp/ignored.jpg")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !result.HasFace || len(result.Center) != 2 || result.Center[0] != 0.5 ||
		result.Box[1] != 0.3 || result.Width != 800 || result.Height != 600 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestPythonProbeAppendsImagePath(t *testing.T) {
	script := writeProbeScript(t, `if [ "$1" != "/tmp/photo.jpg" ]; then exit 9; fi
printf '%s\n' '{"has_face":false,"center":null,"box":null,"width":10,"height":10}'`)
	probe, err := NewPythonProbe(script, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Probe(context.Background(), "/tmp/photo.jpg"); err != nil {
		t.Fatalf("probe: %v", err)
	}
}

func TestPythonProbeErrorCode(t *testing.T) {
	script := writeProbeScript(t, `printf '%s\n' '{"has_face":false,"center":null,"box":null,"width":0,"height":0,"error":"decode_failed"}'`)
	probe, err := NewPythonProbe(script, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = probe.Probe(context.Background(), "/tmp/photo.jpg")
	if err == nil || !strings.Contains(err.Error(), "decode_failed") {
		t.Fatalf("error = %v, want decode_failed", err)
	}
}

func TestPythonProbeInvalidJSON(t *testing.T) {
	script := writeProbeScript(t, `printf 'not json\n'`)
	probe, err := NewPythonProbe(script, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Probe(context.Background(), "/tmp/photo.jpg"); err == nil {
		t.Fatal("expected invalid json error")
	}
}

func TestPythonProbeInvalidGeometry(t *testing.T) {
	script := writeProbeScript(t, `printf '%s\n' '{"has_face":true,"center":null,"box":null,"width":10,"height":10}'`)
	probe, err := NewPythonProbe(script, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := probe.Probe(context.Background(), "/tmp/photo.jpg"); err == nil {
		t.Fatal("expected invalid geometry error")
	}
}

func TestPythonProbeTimeout(t *testing.T) {
	script := writeProbeScript(t, `sleep 5`)
	probe, err := NewPythonProbe(script, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = probe.Probe(context.Background(), "/tmp/photo.jpg")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("error = %v, want timeout", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("probe took too long: %v", time.Since(start))
	}
}

func TestNewPythonProbeDefaults(t *testing.T) {
	probe, err := NewPythonProbe("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(probe.Command, " ") != DefaultCommand {
		t.Fatalf("command = %v", probe.Command)
	}
	if probe.Timeout != DefaultTimeout {
		t.Fatalf("timeout = %v", probe.Timeout)
	}
}

func TestSafeErrorCodeStripsUnsafeChars(t *testing.T) {
	got := safeErrorCode("decode/\nfailed<script>")
	if strings.ContainsAny(got, "/\n<> ") {
		t.Fatalf("unsafe code = %q", got)
	}
}

func TestPythonProbeUnavailableDoesNotLeakCommandPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-python")
	probe, err := NewPythonProbe(missing+" tools/face_probe.py", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = probe.Probe(context.Background(), "/tmp/photo.jpg")
	if !errors.Is(err, ErrProbeUnavailable) {
		t.Fatalf("error = %v, want ErrProbeUnavailable", err)
	}
	if strings.Contains(err.Error(), missing) || strings.Contains(err.Error(), "missing-python") {
		t.Fatalf("error leaked command path: %v", err)
	}
}

func TestPythonProbeNotExecutableIsUnavailable(t *testing.T) {
	script := filepath.Join(t.TempDir(), "not-executable.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	probe, err := NewPythonProbe(script, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = probe.Probe(context.Background(), "/tmp/photo.jpg")
	if !errors.Is(err, ErrProbeUnavailable) {
		t.Fatalf("error = %v, want ErrProbeUnavailable", err)
	}
	if strings.Contains(err.Error(), script) {
		t.Fatalf("error leaked command path: %v", err)
	}
}
