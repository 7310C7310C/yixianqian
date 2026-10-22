package platform

import (
	"net/http"
	"testing"
)

func TestClientIPUntrustedIgnoresHeaders(t *testing.T) {
	trusted, err := ParseTrustedProxies("45.125.16.205,10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set("X-Real-IP", "1.2.3.4")
	h.Set("X-Forwarded-For", "5.6.7.8")
	got := ClientIP("203.0.113.9:443", h, trusted)
	if got != "203.0.113.9" {
		t.Fatalf("got %q, want direct peer", got)
	}
}

func TestClientIPTrustedProxy(t *testing.T) {
	trusted, err := ParseTrustedProxies("45.125.16.205")
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set("X-Real-IP", "1.2.3.4")
	h.Set("X-Forwarded-For", "5.6.7.8, 45.125.16.205")
	got := ClientIP("45.125.16.205:49812", h, trusted)
	if got != "1.2.3.4" {
		t.Fatalf("got %q, want X-Real-IP", got)
	}

	h.Del("X-Real-IP")
	got = ClientIP("45.125.16.205:49812", h, trusted)
	if got != "5.6.7.8" {
		t.Fatalf("got %q, want first XFF", got)
	}
}

func TestClientIPInvalidHeaderFallsBack(t *testing.T) {
	trusted, _ := ParseTrustedProxies("10.0.0.0/8")
	h := http.Header{}
	h.Set("X-Real-IP", "not-an-ip")
	h.Set("X-Forwarded-For", "198.51.100.7")
	got := ClientIP("10.1.2.3:1234", h, trusted)
	if got != "198.51.100.7" {
		t.Fatalf("got %q", got)
	}

	h.Del("X-Forwarded-For")
	got = ClientIP("10.1.2.3:1234", h, trusted)
	if got != "10.1.2.3" {
		t.Fatalf("got %q, want peer", got)
	}
}

func TestClientIPIPv6(t *testing.T) {
	trusted, _ := ParseTrustedProxies("::1")
	h := http.Header{}
	h.Set("X-Real-IP", "2001:db8::1")
	if got := ClientIP("[::1]:8080", h, trusted); got != "2001:db8::1" {
		t.Fatalf("got %q", got)
	}
}

func TestParseTrustedProxies(t *testing.T) {
	if got, err := ParseTrustedProxies(""); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	if _, err := ParseTrustedProxies("not-an-ip"); err == nil {
		t.Fatal("expected error")
	}
	if _, err := ParseTrustedProxies("10.0.0.0/99"); err == nil {
		t.Fatal("expected error")
	}
	got, err := ParseTrustedProxies("45.125.16.205, 10.0.0.0/8")
	if err != nil || len(got) != 2 {
		t.Fatalf("got %v err %v", got, err)
	}
}
