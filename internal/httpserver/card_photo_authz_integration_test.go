package httpserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// createAuthzPhoto inserts a real profile photo (valid UUID) and its fake R2
// objects, so a 200 response means authorization passed AND bytes were served.
func createAuthzPhoto(t *testing.T, env *profileHTTPEnv, userID int64) string {
	t.Helper()
	return createAuthzPhotoAt(t, env, userID, 0)
}

func createAuthzPhotoAt(t *testing.T, env *profileHTTPEnv, userID int64, sortOrder int) string {
	t.Helper()
	id := fmt.Sprintf("aaaaaaaa-bbbb-4ccc-8ddd-%s", randomHTTPDigits(t, 12))
	originalKey := fmt.Sprintf("media/%d/%s.jpg", userID, id)
	thumbKey := fmt.Sprintf("media/%d/%s_thumb.jpg", userID, id)
	if _, err := env.database.CreateMedia(env.ctx, db.Media{
		ID:           id,
		UserID:       userID,
		OriginalName: "photo.jpg",
		ObjectKey:    originalKey,
		ThumbKey:     thumbKey,
		ContentType:  "image/jpeg",
		ByteSize:     1000,
		ThumbSize:    100,
		Width:        600,
		Height:       800,
	}); err != nil {
		t.Fatalf("create media: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO profile_photos (user_id, media_id, sort_order, is_cover)
		 VALUES ($1, $2, $3, $4)`, userID, id, sortOrder, sortOrder == 0); err != nil {
		t.Fatalf("create profile photo: %v", err)
	}
	if err := env.store.Put(env.ctx, originalKey, bytes.Repeat([]byte{0xff}, 1000), "image/jpeg"); err != nil {
		t.Fatalf("put original: %v", err)
	}
	if err := env.store.Put(env.ctx, thumbKey, bytes.Repeat([]byte{0xff}, 100), "image/jpeg"); err != nil {
		t.Fatalf("put thumb: %v", err)
	}
	return id
}

// createSharedAuthzPhoto models migrated data: media.user_id is the upload
// owner while profile_photos.user_id is the gallery/card owner.
func createSharedAuthzPhoto(t *testing.T, env *profileHTTPEnv, mediaOwnerID, galleryOwnerID int64, sortOrder int) string {
	t.Helper()
	id := fmt.Sprintf("bbbbbbbb-cccc-4ddd-8eee-%s", randomHTTPDigits(t, 12))
	originalKey := fmt.Sprintf("media/%d/%s.jpg", mediaOwnerID, id)
	thumbKey := fmt.Sprintf("media/%d/%s_thumb.jpg", mediaOwnerID, id)
	if _, err := env.database.CreateMedia(env.ctx, db.Media{
		ID: id, UserID: mediaOwnerID, OriginalName: "shared.jpg",
		ObjectKey: originalKey, ThumbKey: thumbKey, ContentType: "image/jpeg",
		ByteSize: 1000, ThumbSize: 100, Width: 600, Height: 800,
	}); err != nil {
		t.Fatalf("create shared media: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO profile_photos (user_id, media_id, sort_order, is_cover)
		 VALUES ($1, $2, $3, $4)`, galleryOwnerID, id, sortOrder, sortOrder == 0); err != nil {
		t.Fatalf("create shared profile photo: %v", err)
	}
	if err := env.store.Put(env.ctx, originalKey, bytes.Repeat([]byte{0xff}, 1000), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	if err := env.store.Put(env.ctx, thumbKey, bytes.Repeat([]byte{0xff}, 100), "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	return id
}

func authzUser(t *testing.T, env *profileHTTPEnv, prefix, name, gender, accountStatus, accountKind string) (db.User, *wsClient) {
	t.Helper()
	user, client := env.createUser(t, prefix, name, accountStatus, accountKind)
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE users SET gender = $2, nickname = $3 WHERE id = $1`, user.ID, gender, name); err != nil {
		t.Fatalf("set gender/nickname: %v", err)
	}
	return user, client
}

func expectPhotoStatus(t *testing.T, client *wsClient, path string, want int) {
	t.Helper()
	resp := client.get(path)
	body := readBody(t, resp)
	if resp.StatusCode != want {
		t.Fatalf("%s status = %d, want %d body=%s", path, resp.StatusCode, want, body)
	}
}

func TestIntegrationCardPhotoAuthorizationMatrix(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	owner, ownerClient := authzUser(t, env, "U-AUTHZ-OWNER", "授权照片主人", "女性", "active", "user")
	ownerPhoto := createAuthzPhoto(t, env, owner.ID)

	// 本人始终可看。
	expectPhotoStatus(t, ownerClient, "/cards/photo/"+ownerPhoto+"?size=thumb", http.StatusOK)

	// 完全无关系（且从未拿到牌堆 grant）的活跃用户 → 404，不泄露存在性。
	stranger, strangerClient := authzUser(t, env, "U-AUTHZ-STRANGER", "陌生用户", "男性", "active", "user")
	_ = stranger
	expectPhotoStatus(t, strangerClient, "/cards/photo/"+ownerPhoto+"?size=thumb", http.StatusNotFound)
	expectPhotoStatus(t, strangerClient, "/profile/photo/"+ownerPhoto+"?size=thumb", http.StatusNotFound)

	// 候选池：viewer 请求牌堆后，本次下发的媒体 ID 获得短时 grant。
	viewer, viewerClient := authzUser(t, env, "U-AUTHZ-VIEWER", "候选浏览者", "男性", "active", "user")
	_ = viewer
	cardsResp := viewerClient.get("/legacy/api/cards?limit=50&nickname=" + url.QueryEscape("授权照片主人"))
	if cardsResp.StatusCode != http.StatusOK {
		t.Fatalf("cards status = %d body=%s", cardsResp.StatusCode, readBody(t, cardsResp))
	}
	expectPhotoStatus(t, viewerClient, "/cards/photo/"+ownerPhoto+"?size=thumb", http.StatusOK)
	expectPhotoStatus(t, viewerClient, "/profile/photo/"+ownerPhoto+"?size=thumb", http.StatusOK)

	// 喜欢/被喜欢：不依赖牌堆 grant。
	likeViewer, likeClient := authzUser(t, env, "U-AUTHZ-LIKE", "喜欢者", "男性", "active", "user")
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO likes (initiator_id, target_id, status, like_type, month)
		 VALUES ($1, $2, $3, '实名', '2026-10')`, likeViewer.ID, owner.ID, db.LikeStatusSingle); err != nil {
		t.Fatalf("insert like: %v", err)
	}
	expectPhotoStatus(t, likeClient, "/cards/photo/"+ownerPhoto+"?size=thumb", http.StatusOK)

	// 互喜（反向 like 也覆盖）。
	mutualViewer, mutualClient := authzUser(t, env, "U-AUTHZ-MUTUAL", "互喜者", "男性", "active", "user")
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO likes (initiator_id, target_id, status, like_type, month)
		 VALUES ($1, $2, $3, '实名', '2026-10'), ($2, $1, $3, '实名', '2026-10')`,
		mutualViewer.ID, owner.ID, db.LikeStatusMutual); err != nil {
		t.Fatalf("insert mutual likes: %v", err)
	}
	expectPhotoStatus(t, mutualClient, "/cards/photo/"+ownerPhoto+"?size=thumb", http.StatusOK)

	// 同活动 / 同组 / 同轮：group_results 由报名产生，同活动条件覆盖三者。
	activityID, _ := createHTTPActivity(t, env, 20, false, "")
	activityViewer, activityClient := authzUser(t, env, "U-AUTHZ-ACT", "同活动者", "男性", "active", "user")
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO signups (activity_id, user_id, nickname, status) VALUES ($1, $2, '同活动者', '已报名')`,
		activityID, activityViewer.ID); err != nil {
		t.Fatalf("insert viewer signup: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO signups (activity_id, user_id, nickname, status) VALUES ($1, $2, '授权照片主人', '已报名')`,
		activityID, owner.ID); err != nil {
		t.Fatalf("insert owner signup: %v", err)
	}
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO group_results (activity_id, group_no, user_id, user_name, gender, round)
		 VALUES ($1, 1, $2, '同活动者', '男性', 1), ($1, 1, $3, '授权照片主人', '女性', 1)`,
		activityID, activityViewer.ID, owner.ID); err != nil {
		t.Fatalf("insert group result: %v", err)
	}
	expectPhotoStatus(t, activityClient, "/cards/photo/"+ownerPhoto+"?size=thumb", http.StatusOK)

	// 消息对手方。
	messageViewer, messageClient := authzUser(t, env, "U-AUTHZ-MSG", "留言者", "男性", "active", "user")
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO messages (target_user_id, author_id, content, status) VALUES ($1, $2, '你好', '正常')`,
		owner.ID, messageViewer.ID); err != nil {
		t.Fatalf("insert message: %v", err)
	}
	expectPhotoStatus(t, messageClient, "/cards/photo/"+ownerPhoto+"?size=thumb", http.StatusOK)

	// 管理员权限（任意 RBAC 角色）。
	adminViewer, adminClient := authzUser(t, env, "U-AUTHZ-ADMIN", "管理员", "男性", "active", "user")
	if changed, err := env.database.AssignAdminRole(env.ctx, adminViewer.ID, "super_admin", true); err != nil || !changed {
		t.Fatalf("assign admin role: changed=%v err=%v", changed, err)
	}
	expectPhotoStatus(t, adminClient, "/cards/photo/"+ownerPhoto+"?size=thumb", http.StatusOK)

	// 观察员本身可浏览，但没有关系/牌堆 grant 时不得看照片。
	_, observerClient := authzUser(t, env, "U-AUTHZ-OBS", "观察员", "女性", "observer", "observer")
	expectPhotoStatus(t, observerClient, "/cards/photo/"+ownerPhoto+"?size=thumb", http.StatusNotFound)

	// 禁用账号：账号门禁 403（不是照片存在性 404）。
	disabledUser, disabledClient := authzUser(t, env, "U-AUTHZ-DISABLED", "禁用者", "男性", "active", "user")
	if _, err := env.pool.Exec(env.ctx, `UPDATE users SET account_status = 'disabled' WHERE id = $1`, disabledUser.ID); err != nil {
		t.Fatalf("disable viewer: %v", err)
	}
	expectPhotoStatus(t, disabledClient, "/cards/photo/"+ownerPhoto+"?size=thumb", http.StatusForbidden)

	// 匿名 401；猜测/不存在的 UUID 404。
	anonymous := newWSClient(t, env.server.URL)
	expectPhotoStatus(t, anonymous, "/cards/photo/"+ownerPhoto+"?size=thumb", http.StatusUnauthorized)
	expectPhotoStatus(t, anonymous, "/profile/photo/"+ownerPhoto+"?size=thumb", http.StatusUnauthorized)
	expectPhotoStatus(t, viewerClient, "/cards/photo/00000000-0000-4000-8000-000000000000?size=thumb", http.StatusNotFound)
}

func TestIntegrationPublicProfileHidesUnauthorizedPhotos(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	owner, _ := authzUser(t, env, "U-AUTHZ-PUB-OWNER", "公开页主人", "女性", "active", "user")
	createAuthzPhoto(t, env, owner.ID)

	viewer, viewerClient := authzUser(t, env, "U-AUTHZ-PUB-VIEWER", "公开页候选", "男性", "active", "user")
	_ = viewer
	stranger, strangerClient := authzUser(t, env, "U-AUTHZ-PUB-STRANGER", "公开页陌生人", "男性", "active", "user")
	_ = stranger

	type publicProfile struct {
		Photos      []string `json:"photos"`
		PhotosThumb []string `json:"photos_thumb"`
	}
	read := func(client *wsClient) publicProfile {
		t.Helper()
		resp := client.get("/legacy/api/users/" + strconv.FormatInt(owner.ID, 10) + "/public")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("public profile status = %d body=%s", resp.StatusCode, readBody(t, resp))
		}
		var out publicProfile
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatalf("decode public profile: %v", err)
		}
		return out
	}

	if got := read(strangerClient); len(got.Photos) != 0 || len(got.PhotosThumb) != 0 {
		t.Fatalf("stranger public profile leaked photo URLs: %+v", got)
	}

	// 请求牌堆后，候选的公开资料页才返回照片 URL；图片请求也随 grant 放行。
	cardsResp := viewerClient.get("/legacy/api/cards?limit=50&nickname=" + url.QueryEscape("公开页主人"))
	if cardsResp.StatusCode != http.StatusOK {
		t.Fatalf("cards status = %d body=%s", cardsResp.StatusCode, readBody(t, cardsResp))
	}
	got := read(viewerClient)
	if len(got.Photos) == 0 || len(got.PhotosThumb) == 0 {
		t.Fatalf("candidate public profile missing photos: %+v", got)
	}
	expectPhotoStatus(t, viewerClient, got.PhotosThumb[0], http.StatusOK)

	// 点赞关系不依赖牌堆 grant，也应返回照片。
	likeViewer, likeClient := authzUser(t, env, "U-AUTHZ-PUB-LIKE", "公开页喜欢者", "男性", "active", "user")
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO likes (initiator_id, target_id, status, like_type, month)
		 VALUES ($1, $2, $3, '实名', '2026-10')`, likeViewer.ID, owner.ID, db.LikeStatusSingle); err != nil {
		t.Fatalf("insert like: %v", err)
	}
	if got := read(likeClient); len(got.Photos) == 0 {
		t.Fatalf("like relation public profile missing photos: %+v", got)
	}
}

// Reviewer P1 回归：60 个候选 × 9 张照片，翻两页后已下发的 540 张必须仍为 200。
// grant 按 owner 记账后，per-viewer 上限 200 目标不应因媒体数量被淘汰。
func TestIntegrationGrantOwnerDeckPagination(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	_, viewerClient := authzUser(t, env, "U-GRANT-PAGE-VIEWER", "容量浏览者", "男性", "active", "user")
	suffix := randomHTTPDigits(t, 6)
	prefix := "容量候选" + suffix
	var photos []string
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("%s-%02d", prefix, i)
		owner, _ := authzUser(t, env, fmt.Sprintf("U-GRANT-%s-%02d", suffix, i), name, "女性", "active", "user")
		for j := 0; j < 9; j++ {
			photos = append(photos, createAuthzPhotoAt(t, env, owner.ID, j))
		}
	}
	for _, offset := range []int{0, 50} {
		path := fmt.Sprintf("/legacy/api/cards?limit=50&offset=%d&nickname=%s", offset, url.QueryEscape(prefix))
		resp := viewerClient.get(path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("cards offset %d: status=%d body=%s", offset, resp.StatusCode, readBody(t, resp))
		}
	}
	for _, id := range photos {
		expectPhotoStatus(t, viewerClient, "/cards/photo/"+id+"?size=thumb", http.StatusOK)
	}
}

func TestIntegrationPhotoGrantCrossViewerAndRevocation(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	owner, _ := authzUser(t, env, "U-REVOKE-OWNER", "撤销测试主人", "女性", "active", "user")
	photo := createAuthzPhoto(t, env, owner.ID)

	// 候选 grant 只属于本人；另一个 viewer 不能复用。
	viewer, viewerClient := authzUser(t, env, "U-REVOKE-VIEWER", "撤销候选", "男性", "active", "user")
	_ = viewer
	if resp := viewerClient.get("/legacy/api/cards?limit=50&nickname=" + url.QueryEscape("撤销测试主人")); resp.StatusCode != http.StatusOK {
		t.Fatalf("cards: %d", resp.StatusCode)
	}
	other, otherClient := authzUser(t, env, "U-REVOKE-OTHER", "撤销旁人", "男性", "active", "user")
	_ = other
	expectPhotoStatus(t, viewerClient, "/cards/photo/"+photo+"?size=thumb", http.StatusOK)
	expectPhotoStatus(t, otherClient, "/cards/photo/"+photo+"?size=thumb", http.StatusNotFound)

	// 喜欢删除后即时撤销。
	likeViewer, likeClient := authzUser(t, env, "U-REVOKE-LIKE", "撤销喜欢", "男性", "active", "user")
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO likes (initiator_id, target_id, status, like_type, month)
		 VALUES ($1, $2, $3, '实名', '2026-10')`, likeViewer.ID, owner.ID, db.LikeStatusSingle); err != nil {
		t.Fatal(err)
	}
	expectPhotoStatus(t, likeClient, "/cards/photo/"+photo+"?size=thumb", http.StatusOK)
	if _, err := env.pool.Exec(env.ctx, `DELETE FROM likes WHERE initiator_id=$1 AND target_id=$2`, likeViewer.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	expectPhotoStatus(t, likeClient, "/cards/photo/"+photo+"?size=thumb", http.StatusNotFound)

	// 消息软删除后撤销。
	msgViewer, msgClient := authzUser(t, env, "U-REVOKE-MSG", "撤销消息", "男性", "active", "user")
	var msgID int64
	if err := env.pool.QueryRow(env.ctx,
		`INSERT INTO messages (target_user_id, author_id, content, status)
		 VALUES ($1, $2, '你好', '正常') RETURNING id`, owner.ID, msgViewer.ID).Scan(&msgID); err != nil {
		t.Fatal(err)
	}
	expectPhotoStatus(t, msgClient, "/cards/photo/"+photo+"?size=thumb", http.StatusOK)
	if _, err := env.pool.Exec(env.ctx, `UPDATE messages SET status=$2 WHERE id=$1`, msgID, db.MessageStatusDeleted); err != nil {
		t.Fatal(err)
	}
	expectPhotoStatus(t, msgClient, "/cards/photo/"+photo+"?size=thumb", http.StatusNotFound)

	// 同活动：viewer 取消报名后撤销。
	activityID, _ := createHTTPActivity(t, env, 20, false, "")
	actViewer, actClient := authzUser(t, env, "U-REVOKE-ACT", "撤销活动", "男性", "active", "user")
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO signups (activity_id, user_id, nickname, status) VALUES ($1,$2,'x','已报名'), ($1,$3,'y','已报名')`,
		activityID, actViewer.ID, owner.ID); err != nil {
		t.Fatal(err)
	}
	expectPhotoStatus(t, actClient, "/cards/photo/"+photo+"?size=thumb", http.StatusOK)
	if _, err := env.pool.Exec(env.ctx,
		`UPDATE signups SET status=$3 WHERE activity_id=$1 AND user_id=$2`,
		activityID, actViewer.ID, db.SignupStatusCancelled); err != nil {
		t.Fatal(err)
	}
	expectPhotoStatus(t, actClient, "/cards/photo/"+photo+"?size=thumb", http.StatusNotFound)

	// owner 禁用后，即使保留喜欢关系也必须 404。
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO likes (initiator_id, target_id, status, like_type, month)
		 VALUES ($1, $2, $3, '实名', '2026-10')`, likeViewer.ID, owner.ID, db.LikeStatusSingle); err != nil {
		t.Fatal(err)
	}
	expectPhotoStatus(t, likeClient, "/cards/photo/"+photo+"?size=thumb", http.StatusOK)
	if _, err := env.pool.Exec(env.ctx, `UPDATE users SET account_status='disabled' WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	expectPhotoStatus(t, likeClient, "/cards/photo/"+photo+"?size=thumb", http.StatusNotFound)
}

func TestIntegrationObserverSelfAndDisabledViewerGates(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	observer, observerClient := authzUser(t, env, "U-OBS-SELF", "观察员本人", "女性", "observer", "observer")
	photo := createAuthzPhoto(t, env, observer.ID)
	expectPhotoStatus(t, observerClient, "/profile/photo/"+photo+"?size=thumb", http.StatusOK)
	expectPhotoStatus(t, observerClient, "/cards/photo/"+photo+"?size=thumb", http.StatusOK)

	disabled, disabledClient := authzUser(t, env, "U-DIS-404", "停用账号", "男性", "active", "user")
	if _, err := env.pool.Exec(env.ctx, `UPDATE users SET account_status='disabled' WHERE id=$1`, disabled.ID); err != nil {
		t.Fatal(err)
	}
	expectPhotoStatus(t, disabledClient, "/profile/photo/"+photo+"?size=thumb", http.StatusForbidden)
	expectPhotoStatus(t, disabledClient, "/cards/photo/"+photo+"?size=thumb", http.StatusForbidden)
}

// 共享 media 回归：media.user_id（上传者 A）≠ profile_photos.user_id（画廊 B）。
// viewer 拿到 B 的牌堆 grant 后应能看 B 卡图；非关系 viewer 仍 404。
func TestIntegrationSharedMediaGalleryAuthorization(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	mediaOwner, _ := authzUser(t, env, "U-SHARED-MEDIA", "共享上传者", "男性", "active", "user")
	gallery, _ := authzUser(t, env, "U-SHARED-GALLERY", "共享画廊", "女性", "active", "user")
	photo := createSharedAuthzPhoto(t, env, mediaOwner.ID, gallery.ID, 0)

	viewer, viewerClient := authzUser(t, env, "U-SHARED-VIEWER", "共享候选", "男性", "active", "user")
	_ = viewer
	if resp := viewerClient.get("/legacy/api/cards?limit=50&nickname=" + url.QueryEscape("共享画廊")); resp.StatusCode != http.StatusOK {
		t.Fatalf("cards: %d", resp.StatusCode)
	}
	expectPhotoStatus(t, viewerClient, "/cards/photo/"+photo+"?size=thumb", http.StatusOK)
	expectPhotoStatus(t, viewerClient, "/profile/photo/"+photo+"?size=thumb", http.StatusOK)
	pub := viewerClient.get("/legacy/api/users/" + strconv.FormatInt(gallery.ID, 10) + "/public")
	if pub.StatusCode != http.StatusOK {
		t.Fatalf("shared public profile: %d", pub.StatusCode)
	}
	var pubBody struct {
		Photos []string `json:"photos"`
	}
	if err := json.NewDecoder(pub.Body).Decode(&pubBody); err != nil {
		t.Fatal(err)
	}
	if len(pubBody.Photos) == 0 {
		t.Fatal("shared public profile has no photo URL")
	}
	expectPhotoStatus(t, viewerClient, pubBody.Photos[0], http.StatusOK)

	stranger, strangerClient := authzUser(t, env, "U-SHARED-STRANGER", "共享陌生人", "男性", "active", "user")
	_ = stranger
	expectPhotoStatus(t, strangerClient, "/cards/photo/"+photo+"?size=thumb", http.StatusNotFound)
	expectPhotoStatus(t, strangerClient, "/profile/photo/"+photo+"?size=thumb", http.StatusNotFound)

	// 关系 SQL 也按画廊主人（pp.user_id）判断。
	likeViewer, likeClient := authzUser(t, env, "U-SHARED-LIKE", "共享喜欢", "男性", "active", "user")
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO likes (initiator_id, target_id, status, like_type, month)
		 VALUES ($1, $2, $3, '实名', '2026-10')`, likeViewer.ID, gallery.ID, db.LikeStatusSingle); err != nil {
		t.Fatal(err)
	}
	expectPhotoStatus(t, likeClient, "/cards/photo/"+photo+"?size=thumb", http.StatusOK)

	// 同一 media 同时属于 B、C 两个画廊：viewer 只喜欢 C 时，B 的公开资料页
	// 不得返回该照片（不能复用 media 级关系），但 /cards/photo 仍可按 C 关系取图。
	galleryC, _ := authzUser(t, env, "U-SHARED-GALLERY-C", "共享画廊C", "女性", "active", "user")
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO profile_photos (user_id, media_id, sort_order, is_cover)
		 VALUES ($1, $2, 0, true)`, galleryC.ID, photo); err != nil {
		t.Fatal(err)
	}
	crossViewer, crossClient := authzUser(t, env, "U-SHARED-CROSS", "只喜欢C", "男性", "active", "user")
	if _, err := env.pool.Exec(env.ctx,
		`INSERT INTO likes (initiator_id, target_id, status, like_type, month)
		 VALUES ($1, $2, $3, '实名', '2026-10')`, crossViewer.ID, galleryC.ID, db.LikeStatusSingle); err != nil {
		t.Fatal(err)
	}
	pubB := crossClient.get("/legacy/api/users/" + strconv.FormatInt(gallery.ID, 10) + "/public")
	if pubB.StatusCode != http.StatusOK {
		t.Fatalf("cross public B: %d", pubB.StatusCode)
	}
	var crossBody struct {
		Photos []string `json:"photos"`
	}
	if err := json.NewDecoder(pubB.Body).Decode(&crossBody); err != nil {
		t.Fatal(err)
	}
	if len(crossBody.Photos) != 0 {
		t.Fatalf("B public leaked shared media via C relationship: %v", crossBody.Photos)
	}
	// media-level endpoint is allowed via C's gallery (same photo object).
	expectPhotoStatus(t, crossClient, "/cards/photo/"+photo+"?size=thumb", http.StatusOK)
}

// 250 目标 × 1 图、翻 5 页后回查第 1 页，所有已下发照片仍 200。
func TestIntegrationGrantOwner250TargetsPaginated(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	_, viewerClient := authzUser(t, env, "U-CAP-VIEWER", "容量翻页者", "男性", "active", "user")
	suffix := randomHTTPDigits(t, 6)
	prefix := "容量250" + suffix
	var firstPage []string
	for i := 0; i < 250; i++ {
		name := fmt.Sprintf("%s-%03d", prefix, i)
		owner, _ := authzUser(t, env, fmt.Sprintf("U-CAP-%s-%03d", suffix, i), name, "女性", "active", "user")
		id := createAuthzPhotoAt(t, env, owner.ID, 0)
		if i < 50 {
			firstPage = append(firstPage, id)
		}
	}
	for offset := 0; offset < 250; offset += 50 {
		path := fmt.Sprintf("/legacy/api/cards?limit=50&offset=%d&nickname=%s", offset, url.QueryEscape(prefix))
		resp := viewerClient.get(path)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("cards offset %d: status=%d body=%s", offset, resp.StatusCode, readBody(t, resp))
		}
	}
	for _, id := range firstPage {
		expectPhotoStatus(t, viewerClient, "/cards/photo/"+id+"?size=thumb", http.StatusOK)
	}
}
