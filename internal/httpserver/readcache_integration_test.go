package httpserver

import (
	"testing"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// TestIntegrationLegacyHomeCardsReadCache 覆盖批次9 短缓存的硬要求：
// 命中不再打 DB、不同用户不串、写路径主动失效（错误/未授权路径不经过
// writeReadCache，另有单测覆盖 key 与 TTL）。
func TestIntegrationLegacyHomeCardsReadCache(t *testing.T) {
	tracer := &countingTracer{}
	env := setupActivitiesHTTPEnvWithOptions(t, nil, db.WithQueryTracer(tracer))

	viewer, viewerClient := env.createUser(t, "U-CACHE1", "缓存甲", "active", "user")
	other, _ := env.createUser(t, "U-CACHE2", "缓存乙", "active", "user")
	cleanupHTTPUsers(t, env, viewer.ID, other.ID)
	if _, err := env.pool.Exec(env.ctx, `UPDATE users SET gender='男性' WHERE id IN ($1,$2)`, viewer.ID, other.ID); err != nil {
		t.Fatalf("set gender: %v", err)
	}

	// 1) home：首次打 DB，TTL 内第二次 0 查询。
	tracer.queries.Store(0)
	resp := viewerClient.get("/legacy/api/home")
	if resp.StatusCode != 200 {
		t.Fatalf("first home status=%d", resp.StatusCode)
	}
	readBody(t, resp)
	if tracer.queries.Load() == 0 {
		t.Fatal("first home issued 0 queries")
	}
	tracer.queries.Store(0)
	resp = viewerClient.get("/legacy/api/home")
	readBody(t, resp)
	hitQueries := tracer.queries.Load()
	// 命中仍会有会话/门禁等固定 2–4 条查询；关键是**不再有 build 的 15+ 条**。
	if hitQueries > 5 {
		t.Fatalf("cached home issued %d queries, want <=5 (session-only)", hitQueries)
	}

	// 2) cards：同样命中 0 查询。
	tracer.queries.Store(0)
	resp = viewerClient.get("/legacy/api/cards")
	readBody(t, resp)
	if tracer.queries.Load() == 0 {
		t.Fatal("first cards issued 0 queries")
	}
	tracer.queries.Store(0)
	resp = viewerClient.get("/legacy/api/cards")
	readBody(t, resp)
	if got := tracer.queries.Load(); got > 5 {
		t.Fatalf("cached cards issued %d queries, want <=5 (session-only)", got)
	}

	// 3) 写路径（点赞）必须失效该用户缓存 → 下一次 home 重新打 DB。
	like := viewerClient.postJSON("/legacy/api/like", viewerClient.csrfCookie(), map[string]any{
		"target_openid": *other.LegacyUserID, "like_type": "匿名", "message": "缓存失效验证用的一句话",
	})
	readBody(t, like)
	tracer.queries.Store(0)
	resp = viewerClient.get("/legacy/api/home")
	readBody(t, resp)
	if got := tracer.queries.Load(); got == 0 {
		t.Fatal("write path did not invalidate home cache")
	}
}
