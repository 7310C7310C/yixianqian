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

func TestAdminSessionFreshDisabled(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	app := &App{cfg: config.Config{AdminSessionTTL: 0}, clock: fixedClock{at: now}}
	r := httptest.NewRequest("GET", "/admin", nil)
	if app.adminSessionFresh(r) {
		t.Fatal("anonymous request must not be admin-fresh")
	}

	old := r.WithContext(withSession(r.Context(), &db.Session{CreatedAt: now.Add(-30 * 24 * time.Hour)}))
	if !app.adminSessionFresh(old) {
		t.Fatal("ADMIN_SESSION_TTL=0 must disable the extra admin cap")
	}
}
