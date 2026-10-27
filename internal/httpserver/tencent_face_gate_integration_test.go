package httpserver

import (
	"context"
	"net/http"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/face"
)

// fakeCompliance 模拟腾讯云合规判定，不发起任何外部调用。
type fakeCompliance struct {
	blocked bool
	reason  string
}

func (f fakeCompliance) CheckPhoto(context.Context, string, string) face.ComplianceResult {
	return face.ComplianceResult{Blocked: f.blocked, Reason: f.reason}
}

// 腾讯云判定为非人类照片时：照片仍可上传（v2 语义），但 has_face 必须为
// false，且真实人脸几何不得让门禁通过。
func TestIntegrationTencentComplianceBlocksHasFace(t *testing.T) {
	env := setupProfileHTTPEnv(t)
	owner, ownerClient := env.createUser(t, "U-TCFACE", "合规用户", "active", "user")
	cleanupHTTPUsers(t, env, owner.ID)

	env.app.faceProbe = face.ProbeFunc(func(context.Context, string) (face.Result, error) {
		return face.Result{
			HasFace: true,
			Center:  []float64{0.42, 0.31},
			Box:     []float64{0.2, 0.25},
			Width:   800,
			Height:  600,
		}, nil
	})
	env.app.faceCompliance = fakeCompliance{blocked: true, reason: "animal"}

	csrf := ownerClient.csrfCookie()
	resp := legacyPhotoUpload(t, ownerClient, "/legacy/api/profile/photo", csrf, "", testJPEG(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var upload struct {
		OK      bool     `json:"ok"`
		Photos  []string `json:"photos"`
		HasFace bool     `json:"has_face"`
	}
	legacyJSONDecode(t, resp, &upload)
	if !upload.OK || len(upload.Photos) != 1 {
		t.Fatalf("upload = %+v", upload)
	}
	if upload.HasFace {
		t.Fatal("blocked photo must not satisfy has_face")
	}

	resp = ownerClient.get("/legacy/api/profile")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("profile status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	var profile legacyFaceProfile
	legacyJSONDecode(t, resp, &profile)
	if profile.HasFace {
		t.Fatal("profile has_face must stay false when compliance blocks the only photo")
	}

	// 关掉合规章（模拟 fail-open / 未启用）：同一用户再传一张应恢复 has_face。
	env.app.faceCompliance = nil
	resp = legacyPhotoUpload(t, ownerClient, "/legacy/api/profile/photo", csrf, "", testJPEG(t))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("second upload status = %d body=%s", resp.StatusCode, readBody(t, resp))
	}
	resp = ownerClient.get("/legacy/api/profile")
	legacyJSONDecode(t, resp, &profile)
	if !profile.HasFace {
		t.Fatal("has_face should recover after compliance is disabled")
	}
}
