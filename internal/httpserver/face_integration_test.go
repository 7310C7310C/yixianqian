package httpserver

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
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
	IDValid bool              `json:"id_valid"`
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
	// PM 2026-10-22 裁定：身份证不做 observer 特殊处理，observer 只豁免
	// has_face；id_valid 仍按真实审核结果，不做 grandfather。
	if observerProfile.IDValid {
		t.Fatal("observer must not be grandfathered for id_valid")
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

// registerFaceUser 走完整注册表单（3 张照片），返回新用户与手机号。
func registerFaceUser(t *testing.T, env *profileHTTPEnv, seq string) (dbUserID int64, phone string) {
	t.Helper()
	return registerFaceUserPhotos(t, env, seq, 3)
}

// registerFaceUserPhotos 用指定张数照片走完整注册表单。
func registerFaceUserPhotos(t *testing.T, env *profileHTTPEnv, seq string, photos int) (dbUserID int64, phone string) {
	t.Helper()
	photo := testJPEG(t)
	payload := make([][]byte, 0, photos)
	for i := 0; i < photos; i++ {
		payload = append(payload, photo)
	}
	return registerFaceUserPayload(t, env, seq, payload)
}

// registerFaceUserPayload 用给定图片走完整注册表单。
func registerFaceUserPayload(t *testing.T, env *profileHTTPEnv, seq string, payload [][]byte) (dbUserID int64, phone string) {
	t.Helper()
	return registerFaceUserPayloadTimeout(t, env, seq, payload, 15*time.Second)
}

// registerFaceUserPayloadTimeout 允许大图集成测试放宽 HTTP 客户端超时；
// 服务端同步预算仍由 App 控制。
func registerFaceUserPayloadTimeout(t *testing.T, env *profileHTTPEnv, seq string, payload [][]byte, timeout time.Duration) (dbUserID int64, phone string) {
	t.Helper()
	anon := newWSClient(t, env.server.URL)
	anon.http.Timeout = timeout
	if resp := anon.get("/register"); resp.StatusCode != http.StatusOK {
		t.Fatalf("register page status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	csrf := anon.pageCSRF("/register")
	phone = "1" + randomHTTPDigits(t, 10)
	birthday := "1990-01-01"
	fields := validRegisterValues(t, phone, makeTestIDCard(t, "19900101", seq), birthday)
	fields.Set("csrf_token", csrf)
	resp := postRegisterMultipart(t, anon, fields, payload)
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

// waitProfilePhotoChecked 只断言已检测张数（不限制 has_face，适合真实
// 检测器可能误报的测试）。
func waitProfilePhotoChecked(t *testing.T, env *profileHTTPEnv, userID int64, wantTotal, wantChecked int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var total, checked int
	for {
		if err := env.pool.QueryRow(env.ctx,
			`SELECT count(*), count(*) FILTER (WHERE face_checked)
			   FROM profile_photos WHERE user_id = $1`, userID).
			Scan(&total, &checked); err != nil {
			t.Fatal(err)
		}
		if total == wantTotal && checked == wantChecked {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("photos total=%d checked=%d, want %d/%d",
				total, checked, wantTotal, wantChecked)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitProfilePhotoState 轮询注册照片的检测状态，供后台补检测断言使用。
func waitProfilePhotoState(t *testing.T, env *profileHTTPEnv, userID int64, wantTotal, wantChecked, wantWithFace int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var total, checked, withFace int
	for {
		if err := env.pool.QueryRow(env.ctx,
			`SELECT count(*),
			        count(*) FILTER (WHERE face_checked),
			        count(*) FILTER (WHERE has_face)
			   FROM profile_photos WHERE user_id = $1`, userID).
			Scan(&total, &checked, &withFace); err != nil {
			t.Fatal(err)
		}
		if total == wantTotal && checked == wantChecked && withFace == wantWithFace {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("photos total=%d checked=%d has_face=%d, want %d/%d/%d",
				total, checked, withFace, wantTotal, wantChecked, wantWithFace)
		}
		time.Sleep(10 * time.Millisecond)
	}
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
	// 首张有脸后同步阶段会停止，剩余两张由后台补齐；最终仍应 3/3。
	waitProfilePhotoState(t, env, userID, 3, 3, 3)
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("probe calls = %d, want 3", got)
	}
	if has, err := env.database.ProfileHasFace(env.ctx, userID); err != nil || !has {
		t.Fatalf("ProfileHasFace = %v err=%v, want true", has, err)
	}

	// 无脸结果也必须落 face_checked=true，但 has_face 保持 false。
	atomic.StoreInt32(&calls, 0)
	env.app.faceProbe = face.ProbeFunc(func(context.Context, string) (face.Result, error) {
		atomic.AddInt32(&calls, 1)
		return face.Result{HasFace: false, Width: 800, Height: 600}, nil
	})
	noFaceID, _ := registerFaceUser(t, env, "003")
	waitProfilePhotoState(t, env, noFaceID, 3, 3, 0)
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("no-face probe calls = %d, want 3", got)
	}
	if has, err := env.database.ProfileHasFace(env.ctx, noFaceID); err != nil || has {
		t.Fatalf("ProfileHasFace = %v err=%v, want false", has, err)
	}
}

// 注册 9 张、只有靠后的照片有脸，且同步预算不够：注册不能被阻塞，
// 后台必须继续检查剩余照片并最终得到 has_face=true。
func TestIntegrationRegistrationProbesRemainingPhotosInBackground(t *testing.T) {
	env := setupProfileHTTPEnv(t)

	const photos = 9
	var calls int32
	env.app.faceProbe = face.ProbeFunc(func(ctx context.Context, _ string) (face.Result, error) {
		n := atomic.AddInt32(&calls, 1)
		select {
		case <-time.After(120 * time.Millisecond):
		case <-ctx.Done():
			return face.Result{}, ctx.Err()
		}
		if n == photos {
			return face.Result{
				HasFace: true,
				Center:  []float64{0.5, 0.4},
				Box:     []float64{0.2, 0.3},
				Width:   800,
				Height:  600,
			}, nil
		}
		return face.Result{HasFace: false, Width: 800, Height: 600}, nil
	})
	// 同步预算只够一张多一点，剩余必须由后台补齐。
	env.app.registrationFaceProbeBudget = 250 * time.Millisecond

	photo := testJPEG(t)
	payload := make([][]byte, 0, photos)
	for i := 0; i < photos; i++ {
		payload = append(payload, photo)
	}
	userID, _ := registerFaceUserPayloadTimeout(t, env, "010", payload, 60*time.Second)
	waitProfilePhotoState(t, env, userID, photos, photos, 1)
	if has, err := env.database.ProfileHasFace(env.ctx, userID); err != nil || !has {
		t.Fatalf("ProfileHasFace = %v err=%v, want true", has, err)
	}
}

// 无脸时同步预算内没检查完的照片也必须由后台补齐（全量 checked）。
func TestIntegrationRegistrationProbesNoFaceAllPhotos(t *testing.T) {
	env := setupProfileHTTPEnv(t)

	const photos = 9
	env.app.faceProbe = face.ProbeFunc(func(ctx context.Context, _ string) (face.Result, error) {
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			return face.Result{}, ctx.Err()
		}
		return face.Result{HasFace: false, Width: 800, Height: 600}, nil
	})
	env.app.registrationFaceProbeBudget = 100 * time.Millisecond

	photo := testJPEG(t)
	payload := make([][]byte, 0, photos)
	for i := 0; i < photos; i++ {
		payload = append(payload, photo)
	}
	userID, _ := registerFaceUserPayloadTimeout(t, env, "011", payload, 60*time.Second)
	waitProfilePhotoState(t, env, userID, photos, photos, 0)
	if has, err := env.database.ProfileHasFace(env.ctx, userID); err != nil || has {
		t.Fatalf("ProfileHasFace = %v err=%v, want false", has, err)
	}
}

// 检测命令不可用时注册仍必须成功，照片保持 unchecked 供 backfill 重试。
func TestIntegrationRegistrationProbeFailureDoesNotBlock(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	env.app.faceProbe = face.ProbeFunc(func(context.Context, string) (face.Result, error) {
		return face.Result{}, face.ErrProbeUnavailable
	})

	userID, _ := registerFaceUser(t, env, "012")
	waitProfilePhotoState(t, env, userID, 3, 0, 0)
}

// testLargeJPEG 生成指定尺寸的 JPEG，用于真实 probe 的 12MP 边界测试。
func testLargeJPEG(t *testing.T, width, height int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetRGBA(x, y, color.RGBA{R: uint8(x % 251), G: uint8(y % 241), B: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 82}); err != nil {
		t.Fatalf("encode large jpeg: %v", err)
	}
	return buf.Bytes()
}

// 需要真实 cv2 的运行环境；CI 默认跳过。用法：
//
//	FACE_PROBE_TEST_CMD='/opt/yixianqian-next/venv/bin/python tools/face_probe.py' \
//	FACE_MODEL_PATH=/opt/yixianqian-next/tools/face_detection_yunet.onnx \
//	TEST_DATABASE_URL=... go test ./internal/httpserver -run TestIntegrationRegistrationProbesTwelveMegapixel -v
//
// 场景一：3 张 4000x3000（MEDIA_MAX_PIXELS 上限）必须在注册响应前全部
// checked，直接覆盖 Reviewer B1 的 12MP timeout。
// 场景二：9 张 4000x3000 无脸图，默认 15s 同步预算后由后台补齐，最终
// 全部 checked，证明大图不会漏检。
func TestIntegrationRegistrationProbesTwelveMegapixelPhotos(t *testing.T) {
	cmd := os.Getenv("FACE_PROBE_TEST_CMD")
	if cmd == "" {
		t.Skip("FACE_PROBE_TEST_CMD not set")
	}
	probe, err := face.NewPythonProbe(cmd, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	large := testLargeJPEG(t, 4000, 3000)

	t.Run("three_photos_checked_before_response", func(t *testing.T) {
		env := setupProfileHTTPEnv(t)
		env.app.faceProbe = probe
		payload := [][]byte{large, large, large}
		start := time.Now()
		userID, _ := registerFaceUserPayloadTimeout(t, env, "013", payload, 120*time.Second)
		t.Logf("3x12MP registration elapsed=%s", time.Since(start))
		waitProfilePhotoChecked(t, env, userID, 3, 3, 10*time.Second)
	})

	t.Run("nine_photos_checked_in_background", func(t *testing.T) {
		env := setupProfileHTTPEnv(t)
		env.app.faceProbe = probe
		payload := make([][]byte, 0, 9)
		for i := 0; i < 9; i++ {
			payload = append(payload, large)
		}
		start := time.Now()
		userID, _ := registerFaceUserPayloadTimeout(t, env, "014", payload, 120*time.Second)
		t.Logf("9x12MP registration elapsed=%s", time.Since(start))
		waitProfilePhotoChecked(t, env, userID, 9, 9, 90*time.Second)
	})
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
