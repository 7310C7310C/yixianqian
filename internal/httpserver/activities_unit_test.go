package httpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/7310C7310C/yixianqian/internal/db"
)

func TestActivityErrorCodeMapping(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{db.ErrActivityNotFound, http.StatusNotFound, "activity_not_found"},
		{db.ErrActivityFull, http.StatusConflict, "activity_full"},
		{db.ErrAlreadySignedUp, http.StatusConflict, "already_signed_up"},
		{db.ErrNotSignedUp, http.StatusConflict, "not_signed_up"},
		{db.ErrAlreadyCancelled, http.StatusConflict, "already_cancelled"},
		{db.ErrSigninDisabled, http.StatusBadRequest, "signin_disabled"},
		{db.ErrSigninCodeNotSet, http.StatusConflict, "signin_code_not_set"},
		{db.ErrSigninCodeInvalid, http.StatusBadRequest, "signin_code_invalid"},
		{db.ErrMarkedAbsent, http.StatusConflict, "attendance_marked_absent"},
	}
	for _, tc := range cases {
		status, code, _ := activityActionError(tc.err, "报名")
		if status != tc.status || code != tc.code {
			t.Errorf("activityActionError(%v) = %d/%s, want %d/%s",
				tc.err, status, code, tc.status, tc.code)
		}
	}
	if status, code, msg := activityActionError(&db.ActivityStatusError{Status: "已结束"}, "报名"); status != http.StatusConflict ||
		code != "activity_not_open" || !strings.Contains(msg, "已结束") {
		t.Errorf("status error mapping = %d %s %s", status, code, msg)
	}
}

func TestActivitySigninCodeValidation(t *testing.T) {
	if !validSigninCode("0123") || validSigninCode("123") || validSigninCode("12a4") {
		t.Fatal("validSigninCode must require four digits and preserve leading zeros")
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"code":"0123"}`))
	req.Header.Set("Content-Type", "application/json")
	if got := decodeSigninCode(req); got != "0123" {
		t.Fatalf("decode string code = %q", got)
	}
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"code":1234}`))
	req.Header.Set("Content-Type", "application/json")
	if got := decodeSigninCode(req); got != "1234" {
		t.Fatalf("decode numeric code = %q", got)
	}
	req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"code":true}`))
	req.Header.Set("Content-Type", "application/json")
	if got := decodeSigninCode(req); got != "" {
		t.Fatalf("decode bool code = %q", got)
	}
}
