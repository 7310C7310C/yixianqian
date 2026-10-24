package httpserver

import (
	"context"
	"net/http"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func init() { registerModule(registerAdminMatchingPages) }

func registerAdminMatchingPages(a *App, mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/matching",
		a.adminHTML(adminPermMatchingRun, a.handleAdminMatchingPage))
	mux.HandleFunc("POST /admin/matching/recommend/run",
		a.adminHTML(adminPermMatchingRun, a.handleAdminRecommendRun))
	mux.HandleFunc("GET /admin/matching/group/{id}",
		a.adminHTML(adminPermMatchingRun, a.handleAdminMatchingGroupPage))
	mux.HandleFunc("GET /admin/matching/onetoone/{id}",
		a.adminHTML(adminPermMatchingRun, a.handleAdminMatchingOnetoonePage))
}

type adminMatchingRow struct {
	Activity       db.Activity
	Mode           string
	SignupCount    int
	CheckedInCount int
	GroupRound     int
	OnetooneRound  int
}

type adminMatchingPageData struct {
	Rows      []adminMatchingRow
	Recommend adminRecommendView
}

// adminRecommendView 是「本周推荐位」区块。它刻意不展示必显原因：
// 谁暗恋谁只存服务端（weekly_recommendations.pinned_by_user_id），
// 管理端只看得到覆盖人数、条目数与生成时间。
type adminRecommendView struct {
	Week      string
	Viewers   int
	Entries   int
	Generated string
	Rows      []adminRecommendRow
	Pending   bool
}

type adminRecommendRow struct {
	UserID   int64
	Nickname string
	Count    int
	Updated  string
}

func adminRecommendNotice(code string) string {
	switch code {
	case "recommend":
		return "本周推荐名单已重新生成。"
	case "recommend-failed":
		return "推荐名单生成失败，请稍后重试；旧名单保持不变。"
	default:
		return ""
	}
}

func (a *App) loadAdminRecommendView(ctx context.Context) (adminRecommendView, error) {
	week := db.WeekKey(a.clock.Now())
	summary, err := a.db.WeeklyRecommendationSummary(ctx, week, 20)
	if err != nil {
		return adminRecommendView{}, err
	}
	view := adminRecommendView{
		Week:    summary.Week,
		Viewers: summary.Viewers,
		Entries: summary.Entries,
		Pending: summary.Entries == 0,
	}
	for _, row := range summary.Sample {
		view.Rows = append(view.Rows, adminRecommendRow{
			UserID:   row.UserID,
			Nickname: row.Nickname,
			Count:    row.Count,
			Updated:  db.InSocialTZ(row.UpdatedAt).Format("2006-01-02 15:04"),
		})
	}
	if summary.Entries > 0 {
		view.Generated = db.InSocialTZ(summary.Generated).Format("2006-01-02 15:04")
	}
	return view, nil
}

func adminMatchingMode(activity db.Activity) string {
	switch {
	case activity.GroupEnabled && activity.OnetooneEnabled:
		return "双开"
	case activity.GroupEnabled:
		return "分组"
	case activity.OnetooneEnabled:
		return "一对一"
	default:
		return "未开启"
	}
}

func (a *App) handleAdminMatchingPage(w http.ResponseWriter, r *http.Request, access adminAccess) {
	activities, err := a.db.ListActivities(r.Context())
	if err != nil {
		a.logger(r).Error("list matching activities failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	recommend, err := a.loadAdminRecommendView(r.Context())
	if err != nil {
		a.logger(r).Error("load weekly recommendations failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	stats, err := a.db.AdminActivitySigninStats(r.Context())
	if err != nil {
		a.logger(r).Error("load matching activity stats failed", "error", err)
		a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
		return
	}
	rows := make([]adminMatchingRow, 0, len(activities))
	for _, activity := range activities {
		row := adminMatchingRow{
			Activity:    activity,
			Mode:        adminMatchingMode(activity),
			SignupCount: activity.CurrentSignup,
		}
		if stat, ok := stats[activity.ID]; ok {
			row.SignupCount = stat.SignupCount
			row.CheckedInCount = stat.CheckedInCount
		}
		if activity.GroupEnabled {
			round, err := a.db.LatestGroupRound(r.Context(), activity.ActivityNo)
			if err != nil {
				a.logger(r).Error("load latest group round failed", "error", err,
					"activity_id", activity.ID)
				a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
				return
			}
			row.GroupRound = round
		}
		if activity.OnetooneEnabled {
			round, err := a.db.LatestOnetooneRound(r.Context(), activity.ID)
			if err != nil {
				a.logger(r).Error("load latest onetoone round failed", "error", err,
					"activity_id", activity.ID)
				a.renderError(w, r, http.StatusInternalServerError, "系统暂时无法处理", "请稍后再试。")
				return
			}
			row.OnetooneRound = round
		}
		rows = append(rows, row)
	}
	a.render(w, r, http.StatusOK, "admin_matching", PageData{
		Title:     "匹配控制台",
		User:      &access.User,
		Data:      adminMatchingPageData{Rows: rows, Recommend: recommend},
		Notice:    adminRecommendNotice(r.URL.Query().Get("notice")),
		PageCSS:   "admin.css",
		ActiveNav: "matching",
	})
}

// handleAdminRecommendRun 手动重跑本周推荐名单。生成器本身幂等：同一周重跑
// 只做「剔除失效 + 补空位」，跨周才换批，所以按钮不会把当周名单打乱。
func (a *App) handleAdminRecommendRun(w http.ResponseWriter, r *http.Request, access adminAccess) {
	if err := r.ParseForm(); err != nil {
		a.renderError(w, r, http.StatusBadRequest, "操作失败", "表单格式不正确，请重试。")
		return
	}
	if !a.formCSRFValid(r) {
		a.renderError(w, r, http.StatusForbidden, "操作被拒绝", "表单已过期，请刷新页面后重试。")
		return
	}
	result, err := a.db.GenerateWeeklyRecommendations(r.Context(), a.clock.Now())
	if err != nil {
		a.logger(r).Error("regenerate weekly recommendations failed", "error", err)
		http.Redirect(w, r, "/admin/matching?notice=recommend-failed", http.StatusSeeOther)
		return
	}
	a.audit(r, &access.User.ID, "admin.recommend.run", "weekly_recommendations", result.Week,
		map[string]any{
			"viewers": result.Viewers,
			"entries": result.Entries,
			"rotated": result.Rotated,
			"changed": result.Changed,
		})
	a.logger(r).Info("weekly recommendations regenerated", "week", result.Week,
		"viewers", result.Viewers, "entries", result.Entries,
		"rotated", result.Rotated, "changed", result.Changed)
	http.Redirect(w, r, "/admin/matching?notice=recommend", http.StatusSeeOther)
}

type adminMatchingGroupPageData struct {
	Activity      *db.Activity
	Preview       GroupPreviewView
	Result        AdminGroupResultView
	Roster        matchingRosterView
	Chat          groupChatOverviewView
	ChatRound     int
	SelectedRound int
}

type adminMatchingOnetoonePageData struct {
	Activity      *db.Activity
	Preview       onetoonePreviewView
	Result        onetooneResultView
	Roster        matchingRosterView
	Chat          onetooneChatOverviewView
	ChatRound     int
	SelectedRound int
	FemaleChats   int
}

func adminMatchingNotice(kind, code string) string {
	switch code {
	case "run":
		if kind == "onetoone" {
			return "一对一匹配已完成，结果已刷新。"
		}
		return "分组已完成，结果已刷新。"
	case "reset":
		if kind == "onetoone" {
			return "已清空全部一对一结果，并重设一对一状态。"
		}
		return "已清空全部分组结果，并重设分组状态。"
	default:
		return ""
	}
}

func (a *App) handleAdminMatchingGroupPage(w http.ResponseWriter, r *http.Request, access adminAccess) {
	activity, ok := a.adminActivityByPath(w, r)
	if !ok {
		return
	}
	round := adminQueryInt(r, "round")
	if round < 0 {
		round = 0
	}
	previewRound := adminQueryInt(r, "preview_round")
	if previewRound < 0 {
		previewRound = 0
	}
	preview, err := a.db.PreviewGrouping(r.Context(), activity.ActivityNo, previewRound,
		adminQueryInt(r, "male_per_group"), adminQueryInt(r, "female_per_group"))
	if err != nil {
		a.logger(r).Error("load grouping preview failed", "error", err,
			"activity_id", activity.ID)
		a.renderError(w, r, http.StatusInternalServerError, "读取预览失败", "请稍后再试。")
		return
	}
	result, err := a.db.AdminGroupResult(r.Context(), activity.ActivityNo, round)
	if err != nil {
		a.logger(r).Error("load grouping result failed", "error", err,
			"activity_id", activity.ID)
		a.renderError(w, r, http.StatusInternalServerError, "读取分组结果失败", "请稍后再试。")
		return
	}
	roster, err := a.db.MatchingRoster(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("load matching roster failed", "error", err,
			"activity_id", activity.ID)
		a.renderError(w, r, http.StatusInternalServerError, "读取名单失败", "请稍后再试。")
		return
	}
	chatRound := adminQueryInt(r, "chat_round")
	if chatRound < 0 {
		chatRound = 0
	}
	chat, err := a.buildGroupChatOverview(r.Context(), activity, chatRound)
	if err != nil {
		a.logger(r).Error("load group chat overview failed", "error", err,
			"activity_id", activity.ID)
		a.renderError(w, r, http.StatusInternalServerError, "读取聊天概览失败", "请稍后再试。")
		return
	}
	a.render(w, r, http.StatusOK, "admin_matching_group", PageData{
		Title: "分组控制台",
		User:  &access.User,
		Data: adminMatchingGroupPageData{
			Activity:      &activity,
			Preview:       buildGroupPreviewView(preview),
			Result:        buildAdminGroupResultView(result),
			Roster:        buildMatchingRosterView(activity, roster),
			Chat:          chat,
			ChatRound:     chat.Round,
			SelectedRound: result.Round,
		},
		PageCSS:   "admin.css",
		ActiveNav: "matching",
		Notice:    adminMatchingNotice("group", r.URL.Query().Get("notice")),
	})
}

func (a *App) handleAdminMatchingOnetoonePage(w http.ResponseWriter, r *http.Request, access adminAccess) {
	activity, ok := a.adminActivityByPath(w, r)
	if !ok {
		return
	}
	round := adminQueryInt(r, "round")
	if round < 0 {
		round = 0
	}
	previewRoundParam := adminQueryInt(r, "preview_round")
	if previewRoundParam < 0 {
		previewRoundParam = 0
	}
	femaleChats := adminQueryInt(r, "female_chats")
	if femaleChats <= 0 {
		latest, err := a.db.LatestOnetooneFemaleChats(r.Context(), activity.ID)
		if err != nil {
			a.logger(r).Error("load latest female chats failed", "error", err,
				"activity_id", activity.ID)
			a.renderError(w, r, http.StatusInternalServerError, "读取历史场次失败", "请稍后再试。")
			return
		}
		femaleChats = latest
		if femaleChats <= 0 {
			femaleChats = 3
		}
	}
	previewRound := previewRoundParam
	if previewRound <= 0 {
		latest, err := a.db.LatestOnetooneRound(r.Context(), activity.ID)
		if err != nil {
			a.logger(r).Error("load latest onetoone round failed", "error", err,
				"activity_id", activity.ID)
			a.renderError(w, r, http.StatusInternalServerError, "读取轮次失败", "请稍后再试。")
			return
		}
		previewRound = latest + 1
	}
	preview, err := a.buildOnetoonePreviewView(r.Context(), activity, previewRound, &femaleChats)
	if err != nil {
		a.logger(r).Error("build onetoone preview failed", "error", err,
			"activity_id", activity.ID)
		a.renderError(w, r, http.StatusInternalServerError, "读取预览失败", "请稍后再试。")
		return
	}
	result, err := a.buildOnetooneResultView(r.Context(), activity, round)
	if err != nil {
		a.logger(r).Error("build onetoone result failed", "error", err,
			"activity_id", activity.ID)
		a.renderError(w, r, http.StatusInternalServerError, "读取一对一结果失败", "请稍后再试。")
		return
	}
	roster, err := a.db.MatchingRoster(r.Context(), activity.ID)
	if err != nil {
		a.logger(r).Error("load matching roster failed", "error", err,
			"activity_id", activity.ID)
		a.renderError(w, r, http.StatusInternalServerError, "读取名单失败", "请稍后再试。")
		return
	}
	chatRound := adminQueryInt(r, "chat_round")
	if chatRound < 0 {
		chatRound = 0
	}
	chat, err := a.buildOnetooneChatOverviewView(r.Context(), activity, chatRound)
	if err != nil {
		a.logger(r).Error("build onetoone chat overview failed", "error", err,
			"activity_id", activity.ID)
		a.renderError(w, r, http.StatusInternalServerError, "读取聊天概览失败", "请稍后再试。")
		return
	}
	a.render(w, r, http.StatusOK, "admin_matching_onetoone", PageData{
		Title: "一对一控制台",
		User:  &access.User,
		Data: adminMatchingOnetoonePageData{
			Activity:      &activity,
			Preview:       preview,
			Result:        result,
			Roster:        buildMatchingRosterView(activity, roster),
			Chat:          chat,
			ChatRound:     chat.Round,
			SelectedRound: result.Round,
			FemaleChats:   femaleChats,
		},
		PageCSS:   "admin.css",
		ActiveNav: "matching",
		Notice:    adminMatchingNotice("onetoone", strings.TrimSpace(r.URL.Query().Get("notice"))),
	})
}
