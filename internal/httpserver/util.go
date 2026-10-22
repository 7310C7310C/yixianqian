package httpserver

import (
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/7310C7310C/yixianqian/internal/db"
)

// safeNext only permits same-origin absolute paths.
func safeNext(raw string) string {
	if raw == "" || !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") ||
		strings.Contains(raw, "\\") {
		return "/"
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	for _, r := range raw {
		if r == '\r' || r == '\n' || r == 0 {
			return "/"
		}
	}
	switch {
	case u.Path == "/login", u.Path == "/logout", u.Path == "/activate",
		u.Path == "/media/upload", strings.HasPrefix(u.Path, "/api/"):
		return "/"
	}
	return raw
}

func legacyID(u *db.User) string {
	if u != nil && u.LegacyUserID != nil && *u.LegacyUserID != "" {
		return *u.LegacyUserID
	}
	return "—"
}

func sanitizeFilename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "." || name == "/" {
		return ""
	}
	if utf8.RuneCountInString(name) > 200 {
		runes := []rune(name)
		name = string(runes[:200])
	}
	return name
}

func retryAfterSeconds(d int) string {
	if d < 0 {
		d = 0
	}
	return strconv.Itoa(d)
}
