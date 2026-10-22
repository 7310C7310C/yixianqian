package httpserver

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/7310C7310C/yixianqian/internal/auth"
	"github.com/7310C7310C/yixianqian/internal/db"
)

// passwordChangeFailure 是共用改密流程的失败结果。调用方按自己的输出格式
// 渲染（SSR 表单或 legacy JSON），避免核心逻辑直接写 HTML。
type passwordChangeFailure struct {
	Status  int
	Message string
}

func passwordChangeFail(status int, message string) *passwordChangeFailure {
	return &passwordChangeFailure{Status: status, Message: message}
}

// applyPasswordChange 复用改密校验、Argon2 闸门与数据库写入。成功时返回 nil，
// 由调用方决定跳转或返回 JSON；Status 为 0 表示请求已取消，无需写响应。
func (a *App) applyPasswordChange(w http.ResponseWriter, r *http.Request, user db.User, mustChange bool, current, password, confirm string) *passwordChangeFailure {
	if current == "" || password == "" {
		return passwordChangeFail(http.StatusBadRequest, "请输入目前密码与新密码。")
	}
	if password != confirm {
		return passwordChangeFail(http.StatusBadRequest, "两次输入的新密码不一致。")
	}
	if password == current {
		return passwordChangeFail(http.StatusBadRequest, "新密码不能与目前密码相同。")
	}
	if err := auth.ValidatePassword(password); err != nil {
		return passwordChangeFail(http.StatusBadRequest, passwordPolicyMessage(err))
	}
	hash, err := a.db.GetPasswordHash(r.Context(), user.ID)
	if errors.Is(err, db.ErrNotFound) {
		return passwordChangeFail(http.StatusConflict, "尚未设定密码，请使用启用链接。")
	}
	if err != nil {
		a.logger(r).Error("load password hash failed", "error", err)
		return passwordChangeFail(http.StatusInternalServerError, "系统暂时无法处理，请稍后再试。")
	}
	acquired, busy := a.acquireHashQuiet(r)
	if !acquired {
		if busy {
			return passwordChangeFail(http.StatusTooManyRequests, "目前请求较多，请稍后再试。")
		}
		return &passwordChangeFailure{Status: 0}
	}
	valid, verifyErr := auth.VerifyPassword(current, hash)
	var newHash string
	var hashErr error
	if verifyErr == nil && valid {
		newHash, hashErr = auth.HashPassword(password)
	}
	a.releaseHash()
	if verifyErr != nil {
		a.logger(r).Error("verify current password failed", "error", verifyErr)
		return passwordChangeFail(http.StatusInternalServerError, "系统暂时无法处理，请稍后再试。")
	}
	if !valid {
		return passwordChangeFail(http.StatusBadRequest, "目前密码不正确。")
	}
	if hashErr != nil {
		a.logger(r).Error("hash new password failed", "error", hashErr)
		return passwordChangeFail(http.StatusInternalServerError, "系统暂时无法处理，请稍后再试。")
	}
	var sessionID int64
	if sess, ok := currentSession(r); ok {
		sessionID = sess.ID
	}
	if err := a.db.ChangePassword(r.Context(), user.ID, newHash, sessionID); err != nil {
		a.logger(r).Error("change password failed", "error", err)
		return passwordChangeFail(http.StatusInternalServerError, "系统暂时无法处理，请稍后再试。")
	}
	a.rotateCSRF(w, r)
	a.audit(r, &user.ID, "auth.password.change", "user", strconv.FormatInt(user.ID, 10),
		map[string]any{"forced": mustChange})
	return nil
}
