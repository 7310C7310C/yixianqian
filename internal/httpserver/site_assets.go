package httpserver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"sync"
	"time"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"github.com/7310C7310C/yixianqian/internal/db"
)

const (
	siteAssetAngelQR  = "angel_qr"
	siteAssetCacheTTL = 60 * time.Second
	// maxSiteAssetBytes is also the DB CHECK upper bound in 0012_site_assets.
	maxSiteAssetBytes = 2 << 20
	siteAssetMinSide  = 60
	siteAssetMaxSide  = 2000
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
// credentials or a bucket URL. Five-minute caching lets an upload propagate
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
	w.Header().Set("Cache-Control", "public, max-age=300")
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
	r.Body = http.MaxBytesReader(w, r.Body, maxSiteAssetBytes+(1<<20))
	if err := r.ParseMultipartForm(maxSiteAssetBytes); err != nil {
		a.renderAdminAngelQR(w, r, access, http.StatusRequestEntityTooLarge,
			"图片超过 2MB，请压缩后再上传。")
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
	data, err := io.ReadAll(io.LimitReader(file, maxSiteAssetBytes+1))
	if err != nil {
		a.renderAdminAngelQR(w, r, access, http.StatusBadRequest, "读取图片失败，请重试。")
		return
	}
	if len(data) == 0 {
		a.renderAdminAngelQR(w, r, access, http.StatusBadRequest, "请选择要上传的图片。")
		return
	}
	if len(data) > maxSiteAssetBytes {
		a.renderAdminAngelQR(w, r, access, http.StatusRequestEntityTooLarge,
			"图片超过 2MB，请压缩后再上传。")
		return
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || !qrFormatSupported(format) {
		a.renderAdminAngelQR(w, r, access, http.StatusBadRequest,
			"图片格式不支持，请使用 JPEG/PNG/WebP/GIF/BMP/TIFF。")
		return
	}
	// Reject oversized dimensions before a full decode: a 2MB PNG/JPEG header
	// can claim huge dimensions, and decoding it would allocate width*height.
	if cfg.Width < siteAssetMinSide || cfg.Height < siteAssetMinSide ||
		cfg.Width > siteAssetMaxSide || cfg.Height > siteAssetMaxSide {
		a.renderAdminAngelQR(w, r, access, http.StatusBadRequest,
			"图片尺寸需在 60-2000 像素之间。")
		return
	}
	// Fully decode and re-encode to strip EXIF/metadata (media rule 2.4). PNG
	// stays lossless; JPEG uses q92, which keeps a QR scannable.
	src, decodedFormat, err := image.Decode(bytes.NewReader(data))
	if err != nil || decodedFormat != format {
		a.renderAdminAngelQR(w, r, access, http.StatusBadRequest, "图片无法解码，请重新导出。")
		return
	}
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	var encoded bytes.Buffer
	// QR assets are always stored as PNG: lossless edges scan more reliably
	// than JPEG artifacts, regardless of the uploaded format.
	contentType := "image/png"
	err = png.Encode(&encoded, src)
	if err != nil {
		a.renderAdminAngelQR(w, r, access, http.StatusInternalServerError, "图片处理失败，请重试。")
		return
	}
	if encoded.Len() > maxSiteAssetBytes {
		a.renderAdminAngelQR(w, r, access, http.StatusRequestEntityTooLarge,
			"图片超过 2MB，请压缩后再上传。")
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
