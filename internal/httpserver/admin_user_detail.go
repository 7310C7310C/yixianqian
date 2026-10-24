package httpserver

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/media"
)

func init() { registerModule(registerAdminUserDetailRoutes) }

func registerAdminUserDetailRoutes(a *App, mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/users/{id}", a.adminHTML(adminPermUserReview, a.handleAdminUserDetail))
	// 照片原图由管理台「新窗口查看」直接打开（Sec-Fetch-Dest: document），
	// 媒体例外：保留鉴权与 user.review 权限，顶层 document 也直接串流（不 303）。
	mux.HandleFunc("GET /admin/users/{id}/photos/{mediaID}",
		a.adminHTMLMedia(adminPermUserReview, a.handleAdminUserPhoto))
}

type adminUserDetailField struct {
	Label string
	Value string
}

type adminUserDetailGroup struct {
	Title  string
	Fields []adminUserDetailField
}

type adminUserPhotoView struct {
	Index    int
	MediaID  string
	ThumbURL string
	FullURL  string
	IsCover  bool
}

type adminUserAuditView struct {
	Time           string
	Actor          string
	Action         string
	Note           string
	PreviousStatus string
}

type adminUserDetailPageData struct {
	User          db.AdminUser
	Groups        []adminUserDetailGroup
	Photos        []adminUserPhotoView
	Audit         []adminUserAuditView
	CanManage     bool
	Next          string
	AccountStatus string
	LoginStatus   string
	Roles         string
	Reviewer      string
	ReviewedAt    string
	ReviewNote    string
	PhotoCount    int
	SuperAdmin    bool
}

func (a *App) handleAdminUserDetail(w http.ResponseWriter, r *http.Request, access adminAccess) {
	userID, ok := a.parseAdminPathID(w, r, false)
	if !ok {
		return
	}
	user, err := a.db.GetAdminUser(r.Context(), userID)
	if errors.Is(err, db.ErrNotFound) {
		a.renderError(w, r, http.StatusNotFound, "找不到用户", "此用户不存在。")
		return
	}
	if err != nil {
		a.logger(r).Error("load admin user failed", "error", err, "user_id", userID)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	profile, err := a.db.GetProfile(r.Context(), userID)
	if err != nil {
		a.logger(r).Error("load admin user profile failed", "error", err, "user_id", userID)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	review, err := a.db.GetAdminUserReview(r.Context(), userID)
	if err != nil {
		a.logger(r).Error("load admin user review failed", "error", err, "user_id", userID)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	audit, err := a.db.ListAdminUserAudit(r.Context(), userID, 50)
	if err != nil {
		a.logger(r).Error("load admin user audit failed", "error", err, "user_id", userID)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}

	data := adminUserDetailPageData{
		User:          user,
		CanManage:     containsString(access.Permissions, adminPermUserManage),
		Next:          "/admin/users/" + strconv.FormatInt(userID, 10),
		AccountStatus: adminUserDetailText(adminAccountStatusLabel(user.AccountStatus)),
		LoginStatus:   adminUserDetailText(adminLoginStatusLabel(user.LoginStatus)),
		Roles:         adminUserDetailList(user.Roles),
		Reviewer:      adminUserReviewerLabel(review),
		ReviewedAt:    adminUserDetailTime(review.ReviewedAt),
		ReviewNote:    adminUserDetailText(review.Note),
		Groups:        adminUserDetailGroups(profile),
		Photos:        adminUserPhotoViews(userID, profile.Photos),
		Audit:         adminUserAuditViews(audit),
		SuperAdmin:    access.SuperAdmin,
	}
	data.PhotoCount = len(data.Photos)

	a.render(w, r, http.StatusOK, "admin_user_detail", PageData{
		Title:     "用户详情",
		User:      &access.User,
		Data:      data,
		Notice:    adminUserNotice(r.URL.Query().Get("notice")),
		PageCSS:   "admin_user_detail.css",
		ActiveNav: "users",
	})
}

func (a *App) handleAdminUserPhoto(w http.ResponseWriter, r *http.Request, _ adminAccess) {
	userID, ok := a.parseAdminPathID(w, r, false)
	if !ok {
		return
	}
	mediaID := r.PathValue("mediaID")
	if !validMediaID(mediaID) {
		http.NotFound(w, r)
		return
	}
	m, err := a.db.GetAdminUserPhotoMedia(r.Context(), userID, mediaID)
	if errors.Is(err, db.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.logger(r).Error("load admin user photo failed", "error", err,
			"user_id", userID, "media_id", mediaID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	key, size, contentType := m.ObjectKey, m.ByteSize, m.ContentType
	if r.URL.Query().Get("size") == "thumb" {
		key, size, contentType = m.ThumbKey, m.ThumbSize, "image/jpeg"
	}

	obj, err := a.store.Get(r.Context(), key)
	if errors.Is(err, media.ErrObjectNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		a.logger(r).Error("open admin user photo failed", "error", err,
			"user_id", userID, "media_id", mediaID)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	defer obj.Close()

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusOK)
	if _, err := io.CopyN(w, obj, size); err != nil {
		a.logger(r).Debug("stream admin user photo interrupted", "error", err,
			"user_id", userID, "media_id", mediaID)
	}
}

// adminUserDetailNextPath accepts only a local detail URL as the post-action
// redirect target, so a crafted next value cannot become an open redirect.
var adminUserDetailNextPattern = regexp.MustCompile(`^/admin/users/[0-9]+$`)

func adminUserDetailNextPath(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if !adminUserDetailNextPattern.MatchString(raw) {
		return "", false
	}
	return raw, true
}

func adminUserActionRedirect(w http.ResponseWriter, r *http.Request, notice string) {
	target := adminUsersURLFromForm(r, url.Values{"notice": {notice}})
	if next, ok := adminUserDetailNextPath(r.PostFormValue("next")); ok {
		target = next + "?notice=" + notice
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func adminUserDetailText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "—"
	}
	return value
}

func adminUserDetailList(values []string) string {
	if len(values) == 0 {
		return "—"
	}
	return strings.Join(values, "、")
}

func adminUserDetailTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "—"
	}
	return value.Format("2006-01-02 15:04")
}

func adminUserDetailDate(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "—"
	}
	return value.Format("2006-01-02")
}

func adminUserDetailHeight(height *int) string {
	if height == nil {
		return "—"
	}
	return fmt.Sprintf("%d cm", *height)
}

func adminAccountStatusLabel(status string) string {
	switch status {
	case "pending":
		return "待审核"
	case "active":
		return "已通过"
	case "rejected":
		return "已拒绝"
	case "disabled":
		return "已停用"
	case "matched":
		return "已脱单"
	case "left":
		return "已退出"
	case "observer":
		return "观察员"
	default:
		return status
	}
}

func adminLoginStatusLabel(status string) string {
	switch status {
	case "active":
		return "正常"
	case "disabled":
		return "禁止登录"
	default:
		return status
	}
}

func adminAccountKindLabel(kind string) string {
	switch kind {
	case "user":
		return "会员"
	case "observer":
		return "观察员"
	default:
		return kind
	}
}

func adminUserReviewerLabel(review db.AdminUserReview) string {
	name := strings.TrimSpace(review.ReviewerName)
	legacy := strings.TrimSpace(review.ReviewerLegacyID)
	switch {
	case name != "" && legacy != "":
		return name + "（" + legacy + "）"
	case name != "":
		return name
	case legacy != "":
		return legacy
	default:
		return "—"
	}
}

func adminUserAuditActionLabel(action string) string {
	switch action {
	case "admin.user.approve":
		return "审核通过"
	case "admin.user.reject":
		return "审核拒绝"
	case "admin.user.disable":
		return "停用账号"
	case "admin.user.enable":
		return "恢复账号"
	case "admin.user.temp_password":
		return "生成临时密码"
	default:
		return action
	}
}

func adminUserIDCardValidLabel(profile db.Profile) string {
	if strings.TrimSpace(profile.IDCardLast4) == "" {
		return "—"
	}
	if profile.IDCardValid {
		return "有效"
	}
	return "未核验"
}

func adminUserDetailGroups(p db.Profile) []adminUserDetailGroup {
	return []adminUserDetailGroup{
		{Title: "基本资料", Fields: []adminUserDetailField{
			{Label: "姓名", Value: adminUserDetailText(p.Name)},
			{Label: "昵称", Value: adminUserDetailText(p.Nickname)},
			{Label: "性别", Value: adminUserDetailText(p.Gender)},
			{Label: "生日", Value: adminUserDetailDate(p.Birthday)},
			{Label: "身高", Value: adminUserDetailHeight(p.HeightCM)},
			{Label: "学历", Value: adminUserDetailText(p.Education)},
		}},
		{Title: "教会信息", Fields: []adminUserDetailField{
			{Label: "受洗名", Value: adminUserDetailText(p.BaptismalName)},
			{Label: "教会", Value: adminUserDetailText(p.Church)},
			{Label: "教会小组", Value: adminUserDetailText(p.ChurchGroup)},
			{Label: "教会地点", Value: adminUserDetailText(p.ChurchLocation)},
		}},
		{Title: "背景与家庭", Fields: []adminUserDetailField{
			{Label: "籍贯", Value: adminUserDetailText(p.NativePlace)},
			{Label: "城市", Value: adminUserDetailText(p.City)},
			{Label: "行业", Value: adminUserDetailText(p.Industry)},
			{Label: "职位", Value: adminUserDetailText(p.Position)},
			{Label: "收入", Value: adminUserDetailText(p.Income)},
			{Label: "房", Value: adminUserDetailText(profileSelectWithNote(p.House, p.HouseNoteHave, p.HouseNoteNone))},
			{Label: "车", Value: adminUserDetailText(profileSelectWithNote(p.Driving, p.DrivingNoteHave, p.DrivingNoteNone))},
			{Label: "婚姻", Value: adminUserDetailText(p.Marriage)},
			{Label: "家庭", Value: adminUserDetailText(p.Family)},
			{Label: "与父母同住", Value: adminUserDetailText(p.LiveWithParents)},
		}},
		{Title: "性格与兴趣", Fields: []adminUserDetailField{
			{Label: "性格", Value: adminUserDetailText(p.Personality)},
			{Label: "自我标签", Value: adminUserDetailList(p.SelfTraits)},
			{Label: "爱好", Value: adminUserDetailList(p.SelfHobbies)},
			{Label: "运动", Value: adminUserDetailList(p.SelfSports)},
			{Label: "MBTI", Value: adminUserDetailList(p.MBTI)},
		}},
		{Title: "择偶条件", Fields: []adminUserDetailField{
			{Label: "择偶条件", Value: adminUserDetailText(p.PartnerCriteria)},
			{Label: "期望标签", Value: adminUserDetailList(p.PartnerTraits)},
			{Label: "期望爱好", Value: adminUserDetailList(p.PartnerHobbies)},
			{Label: "期望运动", Value: adminUserDetailList(p.PartnerSports)},
		}},
		{Title: "联系方式与账号", Fields: []adminUserDetailField{
			{Label: "手机", Value: adminUserDetailText(p.Phone)},
			{Label: "微信", Value: adminUserDetailText(p.Wechat)},
			{Label: "身份证后 4 位", Value: adminUserDetailText(p.IDCardLast4)},
			{Label: "身份证是否有效", Value: adminUserIDCardValidLabel(p)},
			{Label: "账号类型", Value: adminUserDetailText(adminAccountKindLabel(p.AccountKind))},
			{Label: "来源", Value: adminUserDetailText(p.ReferralSource)},
			{Label: "是否为孩子", Value: adminUserDetailText(p.IsForChild)},
			{Label: "微信支付", Value: adminUserDetailText(p.WechatPayment)},
			{Label: "联系方式同意", Value: adminUserContactConsentLabel(p.ContactConsent)},
			{Label: "注册时间", Value: adminUserDetailTime(p.RegisteredAt)},
			{Label: "最近活跃", Value: adminUserDetailTime(p.LastActiveAt)},
			{Label: "创建时间", Value: adminUserDetailTime(&p.CreatedAt)},
		}},
	}
}

func adminUserContactConsentLabel(consent bool) string {
	if consent {
		return "已同意"
	}
	return "未同意"
}

func adminUserPhotoViews(userID int64, photos []db.ProfilePhoto) []adminUserPhotoView {
	out := make([]adminUserPhotoView, 0, len(photos))
	prefix := "/admin/users/" + strconv.FormatInt(userID, 10) + "/photos/"
	for i, photo := range photos {
		base := prefix + photo.MediaID
		out = append(out, adminUserPhotoView{
			Index:    i + 1,
			MediaID:  photo.MediaID,
			ThumbURL: base + "?size=thumb",
			FullURL:  base,
			IsCover:  photo.IsCover,
		})
	}
	return out
}

func adminUserAuditViews(entries []db.AdminUserAuditEntry) []adminUserAuditView {
	out := make([]adminUserAuditView, 0, len(entries))
	for _, entry := range entries {
		out = append(out, adminUserAuditView{
			Time: entry.CreatedAt.Format("2006-01-02 15:04"),
			Actor: adminUserReviewerLabel(db.AdminUserReview{
				ReviewerName: entry.ActorName, ReviewerLegacyID: entry.ActorLegacyID,
			}),
			Action:         adminUserAuditActionLabel(entry.Action),
			Note:           adminUserDetailText(entry.Note),
			PreviousStatus: adminUserDetailText(adminAccountStatusLabel(entry.PreviousStatus)),
		})
	}
	return out
}
