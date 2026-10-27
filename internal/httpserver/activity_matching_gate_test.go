package httpserver

import (
	"io/fs"
	"strings"
	"testing"

	webassets "github.com/7310C7310C/yixianqian/web"
)

// TestSPAMatchingEntriesAreGatedByFlags locks the user-side contract the
// backend relies on: activity cards, my activities/my groups, activity detail
// and result buttons only render when group_flag/onetoone_flag is 「是」. The
// backend now returns 「否」 for every ended activity, so these existing gates
// hide the entries without a frontend change.
func TestSPAMatchingEntriesAreGatedByFlags(t *testing.T) {
	checks := map[string][]string{
		"static/legacy/pages/home.render.js": {
			"(act.my_signup && (act.group_flag==='是' || act.onetoone_flag==='是'))",
			"(act.group_flag==='是' && act.group_status==='已完成')",
			"(act.onetoone_flag==='是' && act.onetoone_status==='已完成')",
		},
		"static/legacy/pages/group.render.js": {
			"_ctx.currentActivity.group_flag==='是' || _ctx.currentActivity.onetoone_flag==='是'",
			"_ctx.currentActivity.group_flag==='是' && _ctx.currentActivity.group_status==='已完成'",
			"_ctx.currentActivity.onetoone_flag==='是' && _ctx.currentActivity.onetoone_status==='已完成'",
		},
	}
	for name, needles := range checks {
		body, err := fs.ReadFile(webassets.FS, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		html := string(body)
		for _, needle := range needles {
			if !strings.Contains(html, needle) {
				t.Fatalf("%s missing matching gate %q", name, needle)
			}
		}
	}
}
