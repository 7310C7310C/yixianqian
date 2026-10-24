// Command static_gzip writes gzip -9 siblings for embedded text assets.
//
//	go run ./tools/static_gzip                # (re)generate .gz next to each source
//	go run ./tools/static_gzip -check         # fail if any .gz is missing or decodes to different bytes
//	go run ./tools/static_gzip -check -strict-bytes  # additionally require byte-identical gzip output
//
// -check is toolchain-agnostic on purpose: the flate encoder output differs
// between Go releases (e.g. 1.24 vs 1.27 produce different bytes for the same
// input), so CI must only require that the committed .gz decodes back to the
// current source. -strict-bytes is for local "I just regenerated" checks.
package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var exts = map[string]bool{".js": true, ".css": true, ".html": true, ".svg": true}

func gunzip(compressed []byte) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		return nil, err
	}
	defer func() { _ = zr.Close() }()
	return io.ReadAll(zr)
}

func gzipBytes(src []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	zw.Header.OS = 255
	if _, err := zw.Write(src); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func main() {
	check := flag.Bool("check", false, "only verify existing .gz files are current")
	strict := flag.Bool("strict-bytes", false, "with -check: also require byte-identical gzip output (same Go toolchain)")
	flag.Parse()
	if *strict && !*check {
		fmt.Fprintln(os.Stderr, "-strict-bytes requires -check")
		os.Exit(2)
	}
	root := filepath.Join("web", "static")
	var stale, written, checked int
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !exts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		want, err := gzipBytes(src)
		if err != nil {
			return err
		}
		checked++
		gzPath := path + ".gz"
		got, readErr := os.ReadFile(gzPath)
		matches := readErr == nil
		if matches && *strict {
			matches = bytes.Equal(got, want)
		} else if matches {
			plain, err := gunzip(got)
			matches = err == nil && bytes.Equal(plain, src)
		}
		if matches {
			return nil
		}
		stale++
		if *check {
			if readErr != nil {
				return fmt.Errorf("missing gzip: %s (run go run ./tools/static_gzip)", gzPath)
			}
			if *strict {
				return fmt.Errorf("stale gzip bytes: %s (run go run ./tools/static_gzip)", gzPath)
			}
			return fmt.Errorf("stale gzip: %s decodes to different bytes than %s (run go run ./tools/static_gzip)", gzPath, path)
		}
		if err := os.WriteFile(gzPath, want, 0o644); err != nil {
			return err
		}
		written++
		return nil
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if *check {
		mode := "content"
		if *strict {
			mode = "strict-bytes"
		}
		fmt.Printf("static gzip OK (%d sources, %s)\n", checked, mode)
		return
	}
	fmt.Printf("static gzip written: %d (stale %d, sources %d)\n", written, stale, checked)
}
