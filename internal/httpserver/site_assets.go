package httpserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"sync"
	"time"

	_ "golang.org/x/image/bmp"
	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/media"
)

const (
	siteAssetAngelQR  = "angel_qr"
	siteAssetCacheTTL = 60 * time.Second
	// qrMaxSide bounds the stored QR edge. It is displayed at <=220px and
	// long-pressed to save; 720px keeps scanning reliable without shipping a
	// 500KB image to every register-page view.
	qrMaxSide = 720
	// maxSiteAssetBytes is also the DB CHECK upper bound in 0012_site_assets.
	maxSiteAssetBytes = 2 << 20
	// maxSiteAssetInputBytes follows the photo upload limit; the stored JPEG
	// is still bounded by maxSiteAssetBytes after the 720px downscale.
	maxSiteAssetInputBytes = 10 << 20
	siteAssetMinSide       = 60
	siteAssetMaxSide       = 8000
	siteAssetMaxPixels     = 12_500_000
)

type cachedSiteAsset struct {
	asset   db.SiteAsset
	expires time.Time
}

type siteAssetCache struct {
	mu    sync.RWMutex
	items map[string]cachedSiteAsset
}

func (c *siteAssetCache) get(key string) (db.SiteAsset, bool) {
	c.mu.RLock()
	item, ok := c.items[key]
	c.mu.RUnlock()
	if !ok || time.Now().After(item.expires) {
		return db.SiteAsset{}, false
	}
	return item.asset, true
}

func (c *siteAssetCache) set(key string, asset db.SiteAsset) {
	c.mu.Lock()
	if c.items == nil {
		c.items = make(map[string]cachedSiteAsset)
	}
	c.items[key] = cachedSiteAsset{asset: asset, expires: time.Now().Add(siteAssetCacheTTL)}
	c.mu.Unlock()
}

func (c *siteAssetCache) delete(key string) {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
}

func qrFormatSupported(format string) bool {
	switch format {
	case "jpeg", "png", "gif", "webp", "bmp", "tiff":
		return true
	default:
		return false
	}
}

func init() { registerModule(registerSiteAssets) }

func registerSiteAssets(a *App, mux *http.ServeMux) {
	// Public, unauthenticated: the register page and the review page embed it.
	mux.HandleFunc("GET /angel-qr", a.handleAngelQR)
	mux.HandleFunc("GET /admin/settings/angel-qr",
		a.adminHTML(adminPermContent, a.handleAdminAngelQRPage))
	mux.HandleFunc("POST /admin/settings/angel-qr",
		a.adminHTML(adminPermContent, a.handleAdminAngelQRUpload))
}

// handleAngelQR streams the admin-managed 小天使二维码. The bytes live in
// PostgreSQL (0012_site_assets), so the public read path never needs R2
// credentials or a bucket URL. One-hour caching lets an upload propagate
// quickly while still absorbing repeated register-page views.
func (a *App) handleAngelQR(w http.ResponseWriter, r *http.Request) {
	asset, ok := a.siteAssets.get(siteAssetAngelQR)
	if !ok {
		var err error
		asset, err = a.db.GetSiteAsset(r.Context(), siteAssetAngelQR)
		if errors.Is(err, db.ErrSiteAssetNotFound) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			a.logger(r).Error("load angel qr failed", "error", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		a.siteAssets.set(siteAssetAngelQR, asset)
	}
	etag := `"` + asset.ETag + `"`
	w.Header().Set("Content-Type", asset.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("ETag", etag)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	if _, err := w.Write(asset.Data); err != nil {
		a.logger(r).Debug("write angel qr failed", "error", err)
	}
}

type adminSiteAssetView struct {
	Width     int
	Height    int
	ByteSize  int64
	UpdatedAt string
}

type adminSiteAssetPageData struct {
	Asset   *adminSiteAssetView
	Version string
	Saved   bool
}

func (a *App) handleAdminAngelQRPage(w http.ResponseWriter, r *http.Request, access adminAccess) {
	a.renderAdminAngelQR(w, r, access, http.StatusOK, "")
}

func (a *App) renderAdminAngelQR(w http.ResponseWriter, r *http.Request, access adminAccess, status int, message string) {
	data := adminSiteAssetPageData{Saved: r.URL.Query().Get("saved") == "1"}
	notice := ""
	if data.Saved {
		notice = "二维码已更新。"
	}
	asset, err := a.db.GetSiteAsset(r.Context(), siteAssetAngelQR)
	switch {
	case errors.Is(err, db.ErrSiteAssetNotFound):
	case err != nil:
		a.logger(r).Error("load angel qr for admin failed", "error", err)
		message = "读取二维码失败，请稍后再试。"
	default:
		data.Asset = &adminSiteAssetView{
			Width:     asset.Width,
			Height:    asset.Height,
			ByteSize:  asset.ByteSize,
			UpdatedAt: asset.UpdatedAt.Format("2006-01-02 15:04"),
		}
		data.Version = asset.ETag
	}
	user := access.User
	a.render(w, r, status, "admin_site_assets", PageData{
		Title:     "小天使二维码",
		User:      &user,
		Error:     message,
		Notice:    notice,
		Data:      data,
		PageCSS:   "admin.css",
		ActiveNav: "admin-site-assets",
	})
}

func (a *App) handleAdminAngelQRUpload(w http.ResponseWriter, r *http.Request, access adminAccess) {
	// Same concurrency gate as photo uploads: HEIC conversion spawns a decoder
	// subprocess, so keep at most one media decode in flight per process.
	if !a.acquireMedia(w, r, false) {
		return
	}
	defer a.releaseMedia()
	r.Body = http.MaxBytesReader(w, r.Body, maxSiteAssetInputBytes+(1<<20))
	if err := r.ParseMultipartForm(maxSiteAssetInputBytes); err != nil {
		a.renderAdminAngelQR(w, r, access, http.StatusRequestEntityTooLarge,
			"图片超过 10MB，请压缩后再上传。")
		return
	}
	defer func() { _ = r.MultipartForm.RemoveAll() }()
	if !a.formCSRFValid(r) {
		a.renderAdminAngelQR(w, r, access, http.StatusForbidden, "表单已过期，请重新整理后再试。")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		a.renderAdminAngelQR(w, r, access, http.StatusBadRequest, "请选择要上传的图片。")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxSiteAssetInputBytes+1))
	if err != nil {
		a.renderAdminAngelQR(w, r, access, http.StatusBadRequest, "读取图片失败，请重试。")
		return
	}
	if len(data) == 0 {
		a.renderAdminAngelQR(w, r, access, http.StatusBadRequest, "请选择要上传的图片。")
		return
	}
	if len(data) > maxSiteAssetInputBytes {
		a.renderAdminAngelQR(w, r, access, http.StatusRequestEntityTooLarge,
			"图片超过 10MB，请压缩后再上传。")
		return
	}
	if media.IsHEIC(data) {
		opts := a.heicOptions(r.Context())
		opts.MaxDimension = siteAssetMaxSide
		opts.MaxPixels = siteAssetMaxPixels
		data, err = media.ConvertHEIC(data, opts)
		if err != nil {
			message := "HEIC 处理失败，请重试；仍失败请在相册转成 JPEG 后再上传。"
			if errors.Is(err, media.ErrImageDimensions) {
				message = "图片尺寸需在 60-8000 像素之间。"
			}
			a.renderAdminAngelQR(w, r, access, http.StatusBadRequest, message)
			return
		}
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || !qrFormatSupported(format) {
		a.renderAdminAngelQR(w, r, access, http.StatusBadRequest,
			"图片格式不支持，请使用 JPEG/PNG/WebP/GIF/BMP/TIFF/HEIC。")
		return
	}
	// Reject oversized dimensions before a full decode: a 10MB HEIC/PNG/JPEG header
	// can claim huge dimensions, and decoding it would allocate width*height.
	if cfg.Width < siteAssetMinSide || cfg.Height < siteAssetMinSide ||
		cfg.Width > siteAssetMaxSide || cfg.Height > siteAssetMaxSide ||
		int64(cfg.Width)*int64(cfg.Height) > siteAssetMaxPixels {
		a.renderAdminAngelQR(w, r, access, http.StatusBadRequest,
			"图片尺寸需在 60-8000 像素之间。")
		return
	}
	// Fully decode and re-encode to strip EXIF/metadata (media rule 2.4). The
	// final QR is JPEG q92 after the 720px cap.
	src, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil || decodedFormat != format {
		a.renderAdminAngelQR(w, r, access, http.StatusBadRequest, "图片无法解码，请重新导出。")
		return
	}
	// JPEG has no alpha: transparent QR backgrounds must become white, never
	// black. Flatten before scaling so downscaled edges keep a white base.
	src = flattenOnWhite(src)
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width > qrMaxSide || height > qrMaxSide {
		scale := float64(qrMaxSide) / float64(max(width, height))
		nw := int(float64(width)*scale + 0.5)
		nh := int(float64(height)*scale + 0.5)
		if nw < 1 {
			nw = 1
		}
		if nh < 1 {
			nh = 1
		}
		dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
		xdraw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, bounds, draw.Src, nil)
		src = dst
		width, height = nw, nh
	}
	var encoded bytes.Buffer
	// QR follows the photo policy: unified JPEG q92 after the 720px cap.
	// Scanning is verified against the real QR in staging smoke.
	contentType := "image/jpeg"
	err = jpeg.Encode(&encoded, src, &jpeg.Options{Quality: 92})
	if err != nil {
		a.renderAdminAngelQR(w, r, access, http.StatusInternalServerError, "图片处理失败，请重试。")
		return
	}
	if encoded.Len() > maxSiteAssetBytes {
		a.renderAdminAngelQR(w, r, access, http.StatusRequestEntityTooLarge,
			"图片压缩后仍超过 2MB，请换一张更简单的图片。")
		return
	}
	data = encoded.Bytes()
	sum := sha256.Sum256(data)
	asset := db.SiteAsset{
		Key:         siteAssetAngelQR,
		ContentType: contentType,
		ByteSize:    int64(len(data)),
		Width:       width,
		Height:      height,
		Data:        data,
		ETag:        hex.EncodeToString(sum[:]),
		UpdatedBy:   &access.User.ID,
	}
	if err := a.db.UpsertSiteAsset(r.Context(), asset); err != nil {
		a.logger(r).Error("save angel qr failed", "error", err, "user_id", access.User.ID)
		a.renderAdminAngelQR(w, r, access, http.StatusInternalServerError, "保存失败，请稍后再试。")
		return
	}
	a.siteAssets.delete(siteAssetAngelQR)
	a.audit(r, &access.User.ID, "admin.site_asset.update", "site_asset", siteAssetAngelQR,
		map[string]any{
			"content_type": contentType,
			"byte_size":    len(data),
			"width":        width,
			"height":       height,
		})
	http.Redirect(w, r, "/admin/settings/angel-qr?saved=1", http.StatusSeeOther)
}

// flattenOnWhite composites an image onto an opaque white background so a
// transparent PNG/WebP QR stays scannable after JPEG encoding.
func flattenOnWhite(src image.Image) *image.RGBA {
	bounds := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, bounds.Min, draw.Over)
	return dst
}
