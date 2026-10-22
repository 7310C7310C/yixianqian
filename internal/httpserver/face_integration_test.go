package httpserver

import (
	"context"
	"math"
	"net/http"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/face"
)

type legacyFaceView struct {
	C   []float64 `json:"c"`
	Box []float64 `json:"box"`
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
