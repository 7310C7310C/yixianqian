package tencentface

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png" // 上传原图可能是 PNG
	"io"
	"os"

	xdraw "golang.org/x/image/draw"
)

const (
	// DefaultMaxEdge 是发送给腾讯云前的最长边（v1 用 1080）。
	DefaultMaxEdge = 1080
	// jpegQuality 与 v1 的保存质量一致。
	jpegQuality = 85
	// maxInputBytes 限制读取的临时文件大小（媒体上限 10MiB + 余量）。
	maxInputBytes = 16 << 20
	// maxInputPixels 防止异常大图在解码/缩放时耗尽内存（媒体上限 12.5MP）。
	maxInputPixels = 26_000_000
)

// ErrInvalidImage 表示图片无法解码或尺寸非法；调用方按 fail-open 处理。
var ErrInvalidImage = errors.New("tencentface: invalid image")

// readBounded 读取临时文件，最多 maxInputBytes 字节。
func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, ErrInvalidImage
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, ErrInvalidImage
	}
	return raw, nil
}

// preparePayload 把图片压到最长边 maxEdge 的 JPEG q85（v1 的 _prepare 语义），
// 既降低 API 体积，也避免把 EXIF 等元数据外发。
func preparePayload(raw []byte, maxEdge int) ([]byte, error) {
	if maxEdge <= 0 {
		maxEdge = DefaultMaxEdge
	}
	src, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, ErrInvalidImage
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= 0 || height <= 0 || int64(width)*int64(height) > maxInputPixels {
		return nil, ErrInvalidImage
	}

	dstWidth, dstHeight := width, height
	longest := width
	if height > longest {
		longest = height
	}
	if longest > maxEdge {
		scale := float64(maxEdge) / float64(longest)
		dstWidth = max(1, int(float64(width)*scale))
		dstHeight = max(1, int(float64(height)*scale))
	}

	// 统一铺白底再缩放：PNG 透明区域不会被 JPEG 编码成黑块。
	// ApproxBiLinear 对 1080 级别的标签/活体输入足够，且比 BiLinear 快数倍，
	// 避免在请求路径上花掉整段超时预算。
	dst := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))
	xdraw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.White}, image.Point{}, xdraw.Src)
	xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, bounds, xdraw.Over, nil)

	var buf bytes.Buffer
	buf.Grow(256 << 10)
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: jpegQuality}); err != nil {
		return nil, ErrInvalidImage
	}
	return buf.Bytes(), nil
}
