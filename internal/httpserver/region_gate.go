package httpserver

import (
	"net/http"
	"path"
	"strings"

	"github.com/7310C7310C/yixianqian/internal/db"
	"github.com/7310C7310C/yixianqian/internal/regions"
)

const regionRequiredMessage = "城市信息需要更新，请先在「修改资料」重新选择现居城市和教会所在地。"

// regionGate blocks the app APIs when the stored city/area value is not in the
// vendored dataset. NativePlace is read-only in the profile UI and is therefore
// not part of the gate; registration still requires a dataset value for it.
// Empty values are tolerated because legacy rows often omit them; only a
// non-empty value outside the dataset forces the user to re-select.
func (a *App) regionGate(w http.ResponseWriter, r *http.Request, user db.User) bool {
	requestPath := path.Clean(r.URL.Path)
	if !regionGateApplies(requestPath) || idCardGateAllowed(requestPath) {
		return false
	}
	// Observers only have the ID-card field in their editable schema, so a
	// forced edit could dead-end; they do not appear in the matching cards.
	if user.AccountKind == "observer" || user.AccountStatus == "observer" ||
		!regionInvalid(user.City, user.ChurchLocation) {
		return false
	}
	w.Header().Set("X-Error-Code", "region_required")
	if strings.HasPrefix(requestPath, "/legacy/api/") {
		writeJSON(w, http.StatusForbidden, map[string]any{
			"error": regionRequiredMessage,
			"code":  "region_required",
		})
		return true
	}
	a.renderError(w, r, http.StatusForbidden, "需要更新城市信息", regionRequiredMessage)
	return true
}

func regionGateApplies(path string) bool {
	// Keep /app and /admin reachable: the SPA shows the forced-edit overlay and
	// admins must not be locked out by their own profile data.
	return strings.HasPrefix(path, "/legacy/api/")
}

func regionInvalid(city, churchLocation string) bool {
	if city != "" {
		if _, ok := regions.NormalizeCityValue(city); !ok {
			return true
		}
	}
	if churchLocation == "" {
		return false
	}
	_, ok := regions.NormalizeAreaValue(churchLocation)
	return !ok
}

func regionInvalidProfile(p db.Profile) bool {
	if p.AccountKind == "observer" || p.AccountStatus == "observer" {
		return false
	}
	return regionInvalid(p.City, p.ChurchLocation)
}
