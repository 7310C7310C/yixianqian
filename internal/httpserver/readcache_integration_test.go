package httpserver

import (
	"io"
	"strconv"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// TestIntegrationLegacyHomeCardsReadCache 覆盖批次9 复审要求：
// 命中只剩会话/门禁查询、写后（epoch++）必须重新打 DB、写后数据确实变化、
// 跨用户不串。删掉中间件的 epoch bump 会让「写后 15 条」断言失败。
func TestIntegrationLegacyHomeCardsReadCache(t *testing.T) {
	tracer := &countingTracer{}
	env := setupActivitiesHTTPEnvWithOptions(t, nil, db.WithQueryTracer(tracer))

	viewer, viewerClient := env.createUser(t, "U-CACHE1", "缓存甲", "active", "user")
	target, targetClient := env.createUser(t, "U-CACHE2", "缓存乙", "active", "user")
	cleanupHTTPUsers(t, env, viewer.ID, target.ID)
	mustExec := func(sql string) {
		t.Helper()
		if _, err := env.pool.Exec(env.ctx, sql); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	mustExec(`UPDATE users SET gender='男性' WHERE id IN (` + strconv.FormatInt(viewer.ID, 10) + `)`)
	mustExec(`UPDATE users SET gender='女性' WHERE id IN (` + strconv.FormatInt(target.ID, 10) + `)`)

	getHome := func() (string, int64) {
		t.Helper()
		tracer.queries.Store(0)
		resp := viewerClient.get("/legacy/api/home")
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("home status=%d", resp.StatusCode)
		}
		return string(body), tracer.queries.Load()
	}

	// 1) 首次：聚合口径 15+ 查询；命中：只剩会话/门禁（3–5 条）。
	_, first := getHome()
	if first < 10 {
		t.Fatalf("first home queries=%d, want aggregate >=10", first)
	}
	body1, hit := getHome()
	if hit > 5 {
		t.Fatalf("cached home queries=%d, want <=5 (session-only)", hit)
	}

	// 2) cards 同样：命中不再 build。
	tracer.queries.Store(0)
	resp := viewerClient.get("/legacy/api/cards")
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if tracer.queries.Load() < 5 {
		t.Fatalf("first cards queries=%d, want >=5", tracer.queries.Load())
	}
	tracer.queries.Store(0)
	resp = viewerClient.get("/legacy/api/cards")
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := tracer.queries.Load(); got > 5 {
		t.Fatalf("cached cards queries=%d, want <=5", got)
	}

	// 3) 跨用户不串：新用户必须重新 build。
	tracer.queries.Store(0)
	resp = targetClient.get("/legacy/api/home")
	_, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if got := tracer.queries.Load(); got < 10 {
		t.Fatalf("other user home queries=%d, want >=10 (no cross-user cache)", got)
	}

	// 4) 写请求（epoch++）：点赞后 home/cards 必须重新 build，且内容变化。
	like := viewerClient.postJSON("/legacy/api/like", viewerClient.csrfCookie(), map[string]any{
		"target_openid": *target.LegacyUserID, "like_type": "匿名", "message": "缓存失效验证用的一句话",
	})
	_, _ = io.ReadAll(like.Body)
	like.Body.Close()
	if like.StatusCode >= 400 {
		t.Fatalf("like status=%d", like.StatusCode)
	}
	body2, afterWrite := getHome()
	if afterWrite < 10 {
		t.Fatalf("post-write home queries=%d, want rebuild >=10 (epoch bump missing?)", afterWrite)
	}
	if body1 == body2 {
		t.Fatal("home body unchanged after like (expected liked flags to change)")
	}
}
