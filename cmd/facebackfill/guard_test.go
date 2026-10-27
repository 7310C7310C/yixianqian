package main

import (
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/config"
)

// TestGuardBackfill 覆盖 M1 的两个守卫：-all 必须带合规回填开关；只要
// 声明了合规（ENABLED 或 BACKFILL）就必须有凭证。cron 的日常增量
// （两个开关都没有、不带 -all）必须继续放行。
func TestGuardBackfill(t *testing.T) {
	withCreds := config.Config{TencentSecretID: "id", TencentSecretKey: "key"}
	cases := []struct {
		name    string
		cfg     config.Config
		all     bool
		wantErr []string // nil = 期望放行；否则错误必须包含每个子串
	}{
		{
			name:    "all without backfill flag is refused",
			cfg:     withCreds,
			all:     true,
			wantErr: []string{"-all", "TENCENT_FACE_BACKFILL=true"},
		},
		{
			name:    "all with enabled but not backfill is refused",
			cfg:     config.Config{TencentFaceEnabled: true, TencentSecretID: "id", TencentSecretKey: "key"},
			all:     true,
			wantErr: []string{"-all", "TENCENT_FACE_BACKFILL=true"},
		},
		{
			name: "all with backfill and credentials passes",
			cfg:  config.Config{TencentFaceBackfill: true, TencentSecretID: "id", TencentSecretKey: "key"},
			all:  true,
		},
		{
			name: "incremental cron env passes unchanged",
			cfg:  config.Config{},
		},
		{
			name:    "enabled without credentials is refused",
			cfg:     config.Config{TencentFaceEnabled: true},
			wantErr: []string{"TENCENT_SECRET_ID", "TENCENT_SECRET_KEY"},
		},
		{
			name:    "backfill without credentials is refused",
			cfg:     config.Config{TencentFaceBackfill: true},
			all:     true,
			wantErr: []string{"TENCENT_SECRET_ID", "TENCENT_SECRET_KEY"},
		},
		{
			name: "enabled with credentials passes",
			cfg:  config.Config{TencentFaceEnabled: true, TencentSecretID: "id", TencentSecretKey: "key"},
		},
		{
			name: "backfill with credentials passes",
			cfg:  config.Config{TencentFaceBackfill: true, TencentSecretID: "id", TencentSecretKey: "key"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := guardBackfill(tc.cfg, tc.all)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("guardBackfill() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatal("guardBackfill() = nil, want refusal")
			}
			for _, want := range tc.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("guardBackfill() error %q missing %q", err, want)
				}
			}
			// 守卫错误不得泄露凭证值。
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "AKID") {
				t.Fatalf("guardBackfill() leaks credentials: %q", err)
			}
		})
	}
}
