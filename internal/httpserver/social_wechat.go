package httpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// Mutual wechat exchange status. "ready" means both sides filled a wechat and
// explicitly agreed to exchange it; only then may the API reveal the contact.
const (
	wechatStatusReady        = "ready"
	wechatStatusNeedWechat   = "need_wechat"
	wechatStatusNeedConsent  = "need_consent"
	wechatStatusWaitingOther = "waiting_other"
)

func socialWechatReady(p db.SocialProfile) bool {
	return strings.TrimSpace(p.Wechat) != "" && p.ContactConsent
}

// mutualWechatStatus describes the viewer's own next action, if any, and
// whether the pair is only waiting for the other side. It never exposes the
// other party's contact value.
func mutualWechatStatus(me, other db.SocialProfile) (status string, required, waiting bool) {
	if !socialWechatReady(me) {
		if strings.TrimSpace(me.Wechat) == "" {
			return wechatStatusNeedWechat, true, false
		}
		return wechatStatusNeedConsent, true, false
	}
	if !socialWechatReady(other) {
		// The pair is not ready yet; the viewer may have done their part, so
		// the UI shows a waiting hint rather than the fill form.
		return wechatStatusWaitingOther, true, true
	}
	return wechatStatusReady, false, false
}

// ensureMutualWechatExchanges sends the one-time exchange notification to both
// sides of every mutual pair that is now ready. Dedupe keys make it safe to
// call from read paths (home / likes) and after writes; anonymous likes never
// reach this function because only confirmed mutual pairs are considered.
func (a *App) ensureMutualWechatForUser(ctx context.Context, userID int64) {
	overview, err := a.db.SocialOverview(ctx, userID, a.clock.Now())
	if err != nil {
		a.log.Warn("mutual wechat overview failed", "error", err, "user_id", userID)
		return
	}
	a.ensureMutualWechatExchanges(ctx, overview)
}

func (a *App) ensureMutualWechatExchanges(ctx context.Context, o db.SocialOverview) {
	if !socialWechatReady(o.Me) {
		return
	}
	for _, pair := range uniqueMutualPairs(o) {
		if !socialWechatReady(pair.Other) {
			continue
		}
		low, high := o.Me.ID, pair.Other.ID
		if low > high {
			low, high = high, low
		}
		sides := []struct {
			recipient int64
			other     db.SocialProfile
		}{
			{o.Me.ID, pair.Other},
			{pair.Other.ID, o.Me},
		}
		for _, side := range sides {
			key := fmt.Sprintf("mutual-wechat:%d:%d:%d", low, high, side.recipient)
			_, err := a.db.CreateNotificationOnce(ctx, db.NotificationInput{
				UserID: side.recipient,
				Kind:   "wechat_exchange",
				Title:  "已交换微信号",
				Body: fmt.Sprintf("你和 %s 已相互同意交换微信号：%s，快去加好友吧。",
					side.other.DisplayName(), side.other.Wechat),
				DedupeKey: key,
				Payload:   map[string]any{"other_id": side.other.ID},
			})
			if err != nil {
				a.log.Warn("mutual wechat notification failed", "error", err,
					"user_id", side.recipient)
			}
		}
	}
}
