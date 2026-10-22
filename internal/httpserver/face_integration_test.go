package httpserver

import (
	"bytes"
	"context"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/face"
)

type legacyFaceView struct {
	C   []float64 `json:"c"`
	Box []float64 `json:"box"`
	TB  []float64 `json:"tb"`
	LR  []float64 `json:"lr"`
}

type legacyFaceProfile struct {
	HasFace bool              `json:"has_face"`
	Photos  []string          `json:"photos"`
	Faces   []*legacyFaceView `json:"faces"`
}

func TestIntegrationLegacyFaceUploadAndProfile(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	owner, ownerClient := env.createUser(t, "U-FACEHTTP", "人脸用户", "active", "user")
	empty, emptyClient := env.createUser(t, "U-FACEEMPTY", "无照片用户", "active", "user")
	observer, observerClient := env.createUser(t, "U-FACEOBS", "观察员", "observer", "observer")
	cleanupHTTPUsers(t, env, owner.ID, empty.ID, observer.ID)

	csrf := ownerClient.csrfCookie()

	env.app.faceProbe = face.ProbeFunc(func(context.Context, string) (face.Result, error) {
		return face.Result{
			HasFace: true,
			Center:  []float64{0.42, 0.31},
			Box:     []float64{0.2, 0.25},
			Width:   800,
			Height:  600,
		}, nil
	})

	resp := legacyPhotoUpload(t, ownerClient, "/legacy/api/profile/photo", csrf, "", testJPEG(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first upload status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var firstUpload struct {
		OK      bool     `json:"ok"`
		Photos  []string `json:"photos"`
		HasFace bool     `json:"has_face"`
	}
	legacyJSONDecode(t, resp, &firstUpload)
	if !firstUpload.OK || !firstUpload.HasFace || len(firstUpload.Photos) != 1 {
		t.Fatalf("first upload = %+v", firstUpload)
	}

	resp = ownerClient.get("/legacy/api/profile")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profile status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var profile legacyFaceProfile
	legacyJSONDecode(t, resp, &profile)
	if !profile.HasFace {
		t.Fatal("profile has_face should be true after a detectable upload")
	}
	if len(profile.Faces) != 1 || profile.Faces[0] == nil {
		t.Fatalf("profile faces = %+v", profile.Faces)
	}
	if len(profile.Faces[0].C) != 2 || math.Abs(profile.Faces[0].C[0]-0.42) > 0.001 ||
		len(profile.Faces[0].Box) != 2 || math.Abs(profile.Faces[0].Box[1]-0.25) > 0.001 {
		t.Fatalf("face item = %+v", profile.Faces[0])
	}
	// 黑边裁切未移植：tb/lr 固定为 0，前端按原图构图。
	if len(profile.Faces[0].TB) != 2 || profile.Faces[0].TB[0] != 0 || profile.Faces[0].TB[1] != 0 ||
		len(profile.Faces[0].LR) != 2 || profile.Faces[0].LR[0] != 0 || profile.Faces[0].LR[1] != 0 {
		t.Fatalf("face tb/lr = %+v/%+v, want zeros", profile.Faces[0].TB, profile.Faces[0].LR)
	}

	// 第二张无脸：上传成功、不写入 has_face，faces 对应位置为 null。
	env.app.faceProbe = face.ProbeFunc(func(context.Context, string) (face.Result, error) {
		return face.Result{HasFace: false, Width: 800, Height: 600}, nil
	})
	resp = legacyPhotoUpload(t, ownerClient, "/legacy/api/profile/photo", csrf, "", testJPEG(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second upload status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var secondUpload struct {
		Photos  []string `json:"photos"`
		HasFace bool     `json:"has_face"`
	}
	legacyJSONDecode(t, resp, &secondUpload)
	if !secondUpload.HasFace {
		t.Fatal("existing detectable photo should keep has_face true")
	}

	resp = ownerClient.get("/legacy/api/profile")
	legacyJSONDecode(t, resp, &profile)
	if !profile.HasFace || len(profile.Faces) != 2 {
		t.Fatalf("profile after second upload = %+v", profile)
	}
	if profile.Faces[0] == nil || profile.Faces[1] != nil {
		t.Fatalf("faces order = %+v", profile.Faces)
	}

	// 没有任何 checked 照片时必须是 false，不能沿用旧的硬编码 true。
	resp = emptyClient.get("/legacy/api/profile")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("empty profile status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var emptyProfile legacyFaceProfile
	legacyJSONDecode(t, resp, &emptyProfile)
	if emptyProfile.HasFace {
		t.Fatal("profile without checked photos must not report has_face=true")
	}
	if len(emptyProfile.Faces) != 0 {
		t.Fatalf("empty profile faces = %+v", emptyProfile.Faces)
	}

	// v1 的 user_has_face 对观察员直接放行。
	resp = observerClient.get("/legacy/api/profile")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("observer profile status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var observerProfile legacyFaceProfile
	legacyJSONDecode(t, resp, &observerProfile)
	if !observerProfile.HasFace {
		t.Fatal("observer should bypass the face gate")
	}
}

func TestIntegrationLegacyFaceWriteFailureDoesNotBlockUpload(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	owner, ownerClient := env.createUser(t, "U-FACEFAIL", "检测失败用户", "active", "user")
	cleanupHTTPUsers(t, env, owner.ID)
	csrf := ownerClient.csrfCookie()

	env.app.faceProbe = face.ProbeFunc(func(context.Context, string) (face.Result, error) {
		return face.Result{}, context.DeadlineExceeded
	})

	resp := legacyPhotoUpload(t, ownerClient, "/legacy/api/profile/photo", csrf, "", testJPEG(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload must succeed when probe fails: status = %d body=%s",
			resp.StatusCode, readBody(t, resp))
	}
	var payload struct {
		OK      bool     `json:"ok"`
		Photos  []string `json:"photos"`
		HasFace bool     `json:"has_face"`
	}
	legacyJSONDecode(t, resp, &payload)
	if !payload.OK || len(payload.Photos) != 1 || payload.HasFace {
		t.Fatalf("payload = %+v", payload)
	}

	var checked bool
	if err := env.pool.QueryRow(env.ctx,
		`SELECT face_checked FROM profile_photos WHERE user_id = $1`, owner.ID).Scan(&checked); err != nil {
		t.Fatal(err)
	}
	if checked {
		t.Fatal("failed probe must leave face_checked=false for later backfill")
	}
}

// registerFaceUser 走完整注册表单，返回新用户与仍带 session 的客户端。
func registerFaceUser(t *testing.T, env *profileHTTPEnv, seq string) (dbUserID int64, phone string) {
	t.Helper()
	anon := newWSClient(t, env.server.URL)
	if resp := anon.get("/register"); resp.StatusCode != http.StatusOK {
		t.Fatalf("register page status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	csrf := anon.pageCSRF("/register")
	phone = "1" + randomHTTPDigits(t, 10)
	birthday := "1990-01-01"
	fields := validRegisterValues(t, phone, makeTestIDCard(t, "19900101", seq), birthday)
	fields.Set("csrf_token", csrf)
	resp := postRegisterMultipart(t, anon, fields,
		[][]byte{testJPEG(t), testJPEG(t), testJPEG(t)})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("register status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	user, err := env.database.GetUserByPhone(env.ctx, phone)
	if err != nil {
		t.Fatalf("load registered user: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = env.pool.Exec(cleanupCtx, `DELETE FROM users WHERE phone = $1`, phone)
	})
	return user.ID, phone
}

func TestIntegrationRegistrationProbesPhotos(t *testing.T) {
	env := setupProfileHTTPEnv(t)

	var calls int32
	env.app.faceProbe = face.ProbeFunc(func(context.Context, string) (face.Result, error) {
		atomic.AddInt32(&calls, 1)
		return face.Result{
			HasFace: true,
			Center:  []float64{0.5, 0.4},
			Box:     []float64{0.2, 0.3},
			Width:   800,
			Height:  600,
		}, nil
	})

	userID, _ := registerFaceUser(t, env, "002")
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("probe calls = %d, want 3", got)
	}
	var total, checked, withFace int
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*),
		        count(*) FILTER (WHERE face_checked),
		        count(*) FILTER (WHERE has_face)
		   FROM profile_photos WHERE user_id = $1`, userID).
		Scan(&total, &checked, &withFace); err != nil {
		t.Fatal(err)
	}
	if total != 3 || checked != 3 || withFace != 3 {
		t.Fatalf("photos total=%d checked=%d has_face=%d, want 3/3/3", total, checked, withFace)
	}
	if has, err := env.database.ProfileHasFace(env.ctx, userID); err != nil || !has {
		t.Fatalf("ProfileHasFace = %v err=%v, want true", has, err)
	}

	// 无脸结果也必须落 face_checked=true，但 has_face 保持 false。
	env.app.faceProbe = face.ProbeFunc(func(context.Context, string) (face.Result, error) {
		return face.Result{HasFace: false, Width: 800, Height: 600}, nil
	})
	noFaceID, _ := registerFaceUser(t, env, "003")
	if err := env.pool.QueryRow(env.ctx,
		`SELECT count(*),
		        count(*) FILTER (WHERE face_checked),
		        count(*) FILTER (WHERE has_face)
		   FROM profile_photos WHERE user_id = $1`, noFaceID).
		Scan(&total, &checked, &withFace); err != nil {
		t.Fatal(err)
	}
	if total != 3 || checked != 3 || withFace != 0 {
		t.Fatalf("no-face photos total=%d checked=%d has_face=%d, want 3/3/0",
			total, checked, withFace)
	}
	if has, err := env.database.ProfileHasFace(env.ctx, noFaceID); err != nil || has {
		t.Fatalf("ProfileHasFace = %v err=%v, want false", has, err)
	}
}

func TestIntegrationViewRoleCookieCannotBypassFaceGate(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	owner, ownerClient := env.createUser(t, "U-FACEVIEW", "视角用户", "active", "user")
	cleanupHTTPUsers(t, env, owner.ID)

	base, err := url.Parse(env.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	ownerClient.jar.SetCookies(base, []*http.Cookie{{
		Name: "yxq_view_role", Value: "observer", Path: "/",
	}})

	resp := ownerClient.get("/legacy/api/profile")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profile status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var profile legacyFaceProfile
	legacyJSONDecode(t, resp, &profile)
	if profile.HasFace {
		t.Fatal("view-role cookie must not grant has_face without checked photos")
	}

	resp = ownerClient.get("/legacy/api/user/me")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("user/me status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var me struct {
		HasFace    bool `json:"has_face"`
		IsObserver bool `json:"is_observer"`
	}
	legacyJSONDecode(t, resp, &me)
	if me.HasFace {
		t.Fatal("user/me has_face must ignore yxq_view_role")
	}
	if !me.IsObserver {
		t.Fatal("view-role cookie should still keep the observer UI view")
	}

	// /legacy/api/home 的 user.has_face 同样不能因 cookie 变 true。
	resp = ownerClient.get("/legacy/api/home")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("home status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var home struct {
		User struct {
			HasFace    bool `json:"has_face"`
			IsObserver bool `json:"is_observer"`
		} `json:"user"`
	}
	legacyJSONDecode(t, resp, &home)
	if home.User.HasFace {
		t.Fatal("home has_face must ignore yxq_view_role")
	}
	if !home.User.IsObserver {
		t.Fatal("home should still show the observer view for the cookie")
	}
}

// doLegacyPhotoUpload 不发 t.Fatalf，供并发测试的 goroutine 使用。
func doLegacyPhotoUpload(client *wsClient, csrf string, data []byte) (int, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	header := textproto.MIMEHeader{}
	header.Set("Content-Disposition", `form-data; name="file"; filename="concurrent.jpg"`)
	header.Set("Content-Type", "image/jpeg")
	part, err := w.CreatePart(header)
	if err != nil {
		return 0, err
	}
	if _, err := part.Write(data); err != nil {
		return 0, err
	}
	if err := w.Close(); err != nil {
		return 0, err
	}
	req, err := http.NewRequest(http.MethodPost, client.base+"/legacy/api/profile/photo", &buf)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set(auth.CSRFHeader, csrf)
	resp, err := client.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func TestIntegrationConcurrentUploadsNotLimitedByProbe(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	first, firstClient := env.createUser(t, "U-FACECONC1", "并发甲", "active", "user")
	second, secondClient := env.createUser(t, "U-FACECONC2", "并发乙", "active", "user")
	cleanupHTTPUsers(t, env, first.ID, second.ID)

	// 慢 probe 超过 mediaSem 的 2 秒等待窗口；旧实现会持锁导致第二个上传 429。
	env.app.faceProbe = face.ProbeFunc(func(ctx context.Context, _ string) (face.Result, error) {
		select {
		case <-time.After(2500 * time.Millisecond):
			return face.Result{HasFace: true, Width: 800, Height: 600}, nil
		case <-ctx.Done():
			return face.Result{}, ctx.Err()
		}
	})

	data := testJPEG(t)
	type upload struct {
		client *wsClient
		csrf   string
	}
	uploads := []upload{
		{client: firstClient, csrf: firstClient.csrfCookie()},
		{client: secondClient, csrf: secondClient.csrfCookie()},
	}
	type result struct {
		status int
		err    error
	}
	start := make(chan struct{})
	results := make(chan result, len(uploads))
	var wg sync.WaitGroup
	for _, item := range uploads {
		item := item
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			status, err := doLegacyPhotoUpload(item.client, item.csrf, data)
			results <- result{status: status, err: err}
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	for res := range results {
		if res.err != nil {
			t.Fatalf("upload error: %v", res.err)
		}
		if res.status != http.StatusOK {
			t.Fatalf("concurrent upload status = %d, want 200 (probe must not hold mediaSem)", res.status)
		}
	}
}
