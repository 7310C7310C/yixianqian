package db

import (
	"strings"
	"testing"
	"time"
)

func TestAdminActivityInputValidate(t *testing.T) {
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.Local)
	end := start.Add(24 * time.Hour)
	earlyEnd := start.Add(-time.Hour)

	base := func() AdminActivityInput {
		return AdminActivityInput{
			Name: "秋日徒步", Status: ActivityStatusOpen, MaxSignup: 20,
		}
	}

	t.Run("valid", func(t *testing.T) {
		in := base()
		in.SigninEnabled = true
		in.SigninCode = "0123"
		in.StartAt = &start
		in.EndAt = &end
		if err := in.Validate(); err != nil {
			t.Fatalf("valid input err = %v", err)
		}
	})

	t.Run("group and one-to-one are exclusive", func(t *testing.T) {
		in := base()
		in.GroupEnabled = true
		in.OnetooneEnabled = true
		err := in.Validate()
		if err == nil || !strings.Contains(err.Error(), "不能同时开启") {
			t.Fatalf("dual mode err = %v", err)
		}
	})

	t.Run("signin requires four digits", func(t *testing.T) {
		for _, code := range []string{"", "12", "12345", "12a4", " 123"} {
			in := base()
			in.SigninEnabled = true
			in.SigninCode = code
			if err := in.Validate(); err == nil {
				t.Fatalf("code %q unexpectedly valid", code)
			}
		}
		for _, code := range []string{"0000", "1234", "0123"} {
			in := base()
			in.SigninEnabled = true
			in.SigninCode = code
			if err := in.Validate(); err != nil {
				t.Fatalf("code %q err = %v", code, err)
			}
		}
	})

	t.Run("disabled signin accepts empty code", func(t *testing.T) {
		in := base()
		in.SigninCode = ""
		if err := in.Validate(); err != nil {
			t.Fatalf("disabled signin err = %v", err)
		}
	})

	t.Run("end must not precede start", func(t *testing.T) {
		in := base()
		in.StartAt = &start
		in.EndAt = &earlyEnd
		if err := in.Validate(); err == nil {
			t.Fatal("end before start was accepted")
		}
	})
}

func TestIsFourDigitCode(t *testing.T) {
	valid := []string{"0123", "9999", " 1234 "}
	for _, code := range valid {
		if !IsFourDigitCode(code) {
			t.Fatalf("IsFourDigitCode(%q) = false", code)
		}
	}
	invalid := []string{"", "123", "12345", "12 4", "abcd", "１２３４"}
	for _, code := range invalid {
		if IsFourDigitCode(code) {
			t.Fatalf("IsFourDigitCode(%q) = true", code)
		}
	}
}
