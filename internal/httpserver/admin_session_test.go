package httpserver

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/config"
	"github.com/7310C7310C/yixianqian/internal/db"
)

func TestAdminSessionFresh(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	app := &App{cfg: config.Config{AdminSessionTTL: 8 * time.Hour}, clock: fixedClock{at: now}}
	r := httptest.NewRequest("GET", "/admin", nil)

	fresh := r.WithContext(withSession(r.Context(), &db.Session{CreatedAt: now.Add(-7 * time.Hour)}))
	if !app.adminSessionFresh(fresh) {
		t.Fatal("7h session should be fresh")
	}
	stale := r.WithContext(withSession(r.Context(), &db.Session{CreatedAt: now.Add(-9 * time.Hour)}))
	if app.adminSessionFresh(stale) {
		t.Fatal("9h session should be expired")
	}
}
