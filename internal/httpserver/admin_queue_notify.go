package httpserver

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// notifyAdmins fans one admin-queue notification out to every user holding the
// permission. DedupeKey is scoped per admin so a retry cannot duplicate the
// same queue item for the same operator.
func (a *App) notifyAdmins(r *http.Request, permission, kind, title, body, link, dedupeKey string) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer cancel()
	ids, err := a.db.ListAdminUserIDs(ctx, permission)
	if err != nil {
		a.logger(r).Error("list admins for notification failed", "error", err, "permission", permission)
		return
	}
	for _, adminID := range ids {
		if _, err := a.db.CreateNotificationOnce(ctx, db.NotificationInput{
			UserID:    adminID,
			Kind:      kind,
			Title:     title,
			Body:      body,
			Link:      link,
			DedupeKey: fmt.Sprintf("%s:%d", dedupeKey, adminID),
		}); err != nil {
			a.logger(r).Warn("create admin queue notification failed", "error", err,
				"admin_id", adminID, "kind", kind)
		}
	}
}
