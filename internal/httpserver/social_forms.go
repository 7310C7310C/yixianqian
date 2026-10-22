package httpserver

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// cardsReturnURL keeps the submitted card page position (offset/seed/index)
// and filter query while replacing any previous flash code.
func cardsReturnURL(r *http.Request, codeKey, code string) string {
	values := url.Values{}
	raw := strings.TrimSpace(r.PostFormValue("return_to"))
	if parsed, err := url.Parse(raw); err == nil && !parsed.IsAbs() && parsed.Host == "" && parsed.Path == "/cards" {
		values = parsed.Query()
	}
	for _, key := range []string{"offset", "seed", "index"} {
		value := strings.TrimSpace(r.PostFormValue(key))
		if value == "" {
			continue
		}
		if key == "seed" {
			if len(value) <= 64 {
				values.Set(key, value)
			}
			continue
		}
		if n, err := strconv.Atoi(value); err == nil && n >= 0 && n <= 100000 {
			values.Set(key, strconv.Itoa(n))
		}
	}
	for _, key := range []string{"liked", "mutual", "praised", "unpraised", "like_error", "praise_error", "feedback"} {
		values.Del(key)
	}
	if codeKey != "" {
		values.Set(codeKey, code)
	}
	if len(values) == 0 {
		return "/cards"
	}
	return "/cards?" + values.Encode()
}
