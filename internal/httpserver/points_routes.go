package httpserver

import (
	"net/http"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
)

func init() { registerModule(registerPoints) }

// pointsRedeemLimiter mirrors the v1 H5 limit: 20 redemption attempts/minute.
var pointsRedeemLimiter = auth.NewLimiter(20, time.Minute)

func registerPoints(a *App, mux *http.ServeMux) {

}

// pointsViewerAPI applies the same browse/active gates as the social module.
// settlePointsForViewer credits due invite rewards before rendering balances.
// A future cron/worker should also call db.SettleDueInvites globally so users
// who never open the points pages still settle on time.
func (a *App) settlePointsForViewer(r *http.Request, userID int64) {
	if _, _, err := a.db.SettleDueInvitesForInviter(r.Context(), userID, 200); err != nil {
		a.logger(r).Warn("settle viewer invites failed", "error", err, "user_id", userID)
	}
}

func (a *App) pointsViewerAPI(w http.ResponseWriter, r *http.Request, active bool) (db.SocialViewer, bool) {
	user, ok := a.requireUserAPI(w, r)
	if !ok {
		return db.SocialViewer{}, false
	}
	viewer, ok := a.socialViewerAPI(w, r, user)
	if !ok {
		return db.SocialViewer{}, false
	}
	if !a.socialAPIGate(w, r, viewer, active) {
		return db.SocialViewer{}, false
	}
	return viewer, true
}

func (a *App) pointsViewerHTML(w http.ResponseWriter, r *http.Request, active bool) (db.User, db.SocialViewer, bool) {
	user, ok := a.requireUserHTML(w, r)
	if !ok {
		return db.User{}, db.SocialViewer{}, false
	}
	viewer, ok := a.socialViewerHTML(w, r, user)
	if !ok {
		return db.User{}, db.SocialViewer{}, false
	}
	if !a.socialHTMLGate(w, r, viewer, active) {
		return db.User{}, db.SocialViewer{}, false
	}
	return user, viewer, true
}
