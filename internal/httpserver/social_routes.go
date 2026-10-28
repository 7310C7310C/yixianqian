package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
)

func init() { registerModule(registerSocial) }

// social-rate limits mirror the v1 handler limits. They are process-wide and
// keyed by user id, which is enough for the current single-process deployment.
var (
	socialCardLimiter   = auth.NewLimiter(60, time.Minute)
	socialLikeLimiter   = auth.NewLimiter(10, time.Minute)
	socialPraiseLimiter = auth.NewLimiter(20, time.Minute)
)

func registerSocial(a *App, mux *http.ServeMux) {
	mux.HandleFunc("GET /cards/photo/{id}", a.handleCardPhoto)

}

// socialAccountState applies the v1 account gates. active=true is the stricter
// "single user may interact" gate; active=false is the browse gate.
func (a *App) socialViewerAPI(w http.ResponseWriter, r *http.Request, user db.User) (db.SocialViewer, bool) {
	viewer, err := a.db.GetSocialViewer(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		writeAPIError(w, http.StatusForbidden, "account_unavailable", "账号目前无法使用，请联络管理员。")
		return db.SocialViewer{}, false
	}
	if err != nil {
		a.logger(r).Error("load social viewer failed", "error", err, "user_id", user.ID)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "暂时无法处理，请稍后再试。")
		return db.SocialViewer{}, false
	}
	return viewer, true
}

func (a *App) socialViewerHTML(w http.ResponseWriter, r *http.Request, user db.User) (db.SocialViewer, bool) {
	viewer, err := a.db.GetSocialViewer(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		a.renderError(w, r, http.StatusForbidden, "无法使用", "账号目前无法使用，请联络管理员。")
		return db.SocialViewer{}, false
	}
	if err != nil {
		a.logger(r).Error("load social viewer failed", "error", err, "user_id", user.ID)
		a.renderError(w, r, http.StatusInternalServerError, "暂时无法处理", "请稍后再试。")
		return db.SocialViewer{}, false
	}
	return viewer, true
}

func socialAccountState(u db.SocialViewer, active bool) (code, message string, ok bool) {
	if u.Status != "active" {
		return "account_disabled", "账号已停用，请联络管理员。", false
	}
	if u.AccountKind == "observer" || u.AccountStatus == "observer" {
		if active {
			return "observer_readonly", "观察员账号仅可浏览，不能进行此操作。", false
		}
		return "", "", true
	}
	switch u.AccountStatus {
	case "active":
		return "", "", true
	case "matched":
		if active {
			return "account_matched", "你目前为已脱单状态，请先在「我的」页恢复单身。", false
		}
		return "", "", true
	case "pending":
		return "account_pending", "资料审核中，通过后即可使用一线牵。", false
	case "rejected":
		return "account_rejected", "很抱歉，你的资料未通过审核，如有疑问请联络管理员。", false
	case "left":
		return "account_left", "你已暂时退出相亲市场，如需恢复请联络管理员。", false
	default:
		return "account_unavailable", "账号目前无法使用，请联络管理员。", false
	}
}

func (a *App) socialAPIGate(w http.ResponseWriter, r *http.Request, u db.SocialViewer, active bool) bool {
	code, message, ok := socialAccountState(u, active)
	if !ok {
		writeAPIError(w, http.StatusForbidden, code, message)
		return false
	}
	return true
}

func (a *App) socialHTMLGate(w http.ResponseWriter, r *http.Request, u db.SocialViewer, active bool) bool {
	code, message, ok := socialAccountState(u, active)
	if !ok {
		_ = code
		a.renderError(w, r, http.StatusForbidden, "无法使用", message)
		return false
	}
	return true
}

func (a *App) socialRateLimit(w http.ResponseWriter, r *http.Request, u db.SocialViewer,
	limiter *auth.Limiter, prefix string, api bool) bool {
	ok, retry := limiter.Allow(fmt.Sprintf("%s:%d", prefix, u.ID))
	if ok {
		return true
	}
	w.Header().Set("Retry-After", retryAfterSeconds(int(retry.Seconds())+1))
	if api {
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "操作过于频繁，请稍后再试。")
	} else {
		a.renderError(w, r, http.StatusTooManyRequests, "操作过于频繁", "请稍后再试。")
	}
	return false
}

func (a *App) socialAPICSRF(w http.ResponseWriter, r *http.Request) bool {
	if !a.headerCSRFValid(r) {
		writeAPIError(w, http.StatusForbidden, "csrf_failed", "缺少或无效的 CSRF token。")
		return false
	}
	return true
}

func (a *App) resolveSocialTarget(ctx context.Context, targetID int64, legacyOpenID string) (db.SocialProfile, error) {
	if targetID > 0 {
		return a.db.GetSocialProfile(ctx, targetID)
	}
	if openID := strings.TrimSpace(legacyOpenID); openID != "" {
		p, err := a.db.GetSocialProfileByLegacyOpenID(ctx, openID)
		if err == nil || !errors.Is(err, db.ErrNotFound) {
			return p, err
		}
		if id, parseErr := strconv.ParseInt(openID, 10, 64); parseErr == nil && id > 0 {
			return a.db.GetSocialProfile(ctx, id)
		}
		return db.SocialProfile{}, err
	}
	return db.SocialProfile{}, db.ErrNotFound
}

func (a *App) handleCardPhoto(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		http.Error(w, "需要登录", http.StatusUnauthorized)
		return
	}
	viewer, err := a.db.GetSocialViewer(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if _, _, allowed := socialAccountState(viewer, false); !allowed {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	if !validMediaID(id) {
		http.NotFound(w, r)
		return
	}
	// 默认拒绝：只有本进程刚下发给该 viewer 的牌堆/详情照片（短时 grant）或
	// 本人/管理员/点赞/同活动/消息对手方等持久关系才放行；不存在与无权都回 404。
	var m db.Media
	base, err := a.db.GetCardPhotoBase(r.Context(), id, user.ID)
	if err == nil {
		granted := false
		if a.photoGrants != nil {
			for _, ownerID := range base.GalleryUserIDs {
				if a.photoGrants.AllowedOwner(user.ID, ownerID) {
					granted = true
					break
				}
			}
		}
		if granted {
			m = base.Media
		} else {
			m, err = a.db.GetCardPhotoForViewer(r.Context(), id, user.ID)
		}
	}
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.logger(r).Error("load card photo failed", "error", err, "media_id", id)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	switch r.URL.Query().Get("size") {
	case "hero":
		a.serveMediaHero(w, r, mediaObject{
			ID:         m.ID,
			ObjectKey:  m.ObjectKey,
			ObjectSize: m.ByteSize,
			ObjectType: m.ContentType,
			ThumbKey:   m.ThumbKey,
			ThumbSize:  m.ThumbSize,
		})
	case "thumb":
		a.serveMediaObject(w, r, mediaObject{
			ID:         m.ID,
			ObjectKey:  m.ObjectKey,
			ObjectSize: m.ByteSize,
			ObjectType: m.ContentType,
			ThumbKey:   m.ThumbKey,
			ThumbSize:  m.ThumbSize,
		}, true)
	default:
		a.serveMediaObject(w, r, mediaObject{
			ID:         m.ID,
			ObjectKey:  m.ObjectKey,
			ObjectSize: m.ByteSize,
			ObjectType: m.ContentType,
			ThumbKey:   m.ThumbKey,
			ThumbSize:  m.ThumbSize,
		}, false)
	}
}
