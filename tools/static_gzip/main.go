// Command static_gzip writes gzip -9 siblings for embedded text assets.
//
//	go run ./tools/static_gzip          # (re)generate .gz next to each source
//	go run ./tools/static_gzip -check   # fail if any .gz is missing/stale
package main

import (
	"bytes"
	"compress/gzip"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

var exts = map[string]bool{".js": true, ".css": true, ".html": true, ".svg": true}

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
	flag.Parse()
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
		if readErr == nil && bytes.Equal(got, want) {
			return nil
		}
		stale++
		if *check {
			return fmt.Errorf("stale gzip: %s (run go run ./tools/static_gzip)", gzPath)
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
		fmt.Printf("static gzip OK (%d sources, %d written)\n", checked, written)
		return
	}
	fmt.Printf("static gzip written: %d (stale %d, sources %d)\n", written, stale, checked)
}
