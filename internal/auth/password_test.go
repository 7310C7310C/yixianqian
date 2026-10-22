package auth

import (
	"errors"
	"strings"
	"testing"
)

var testParams = Params{
	Memory:      8 * 1024,
	Iterations:  1,
	Parallelism: 1,
	SaltLength:  16,
	KeyLength:   32,
}

func TestValidatePassword(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  error
	}{
		{"too short", "short12", ErrPasswordTooShort},
		{"minimum", "12345678", nil},
		{"eight runes", "密码密码密码密码", nil},
		{"too long", strings.Repeat("a", MaxPasswordLength+1), ErrPasswordTooLong},
		{"control char", "abcde123\x00", ErrPasswordInvalid},
		{"empty", "", ErrPasswordTooShort},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePassword(tc.password)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestHashVerifyRoundTrip(t *testing.T) {
	hash, err := hashPassword("correct horse battery staple", testParams)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("unexpected PHC prefix: %s", hash)
	}
	ok, err := VerifyPassword("correct horse battery staple", hash)
	if err != nil || !ok {
		t.Fatalf("verify correct: ok=%v err=%v", ok, err)
	}
	ok, err = VerifyPassword("wrong password", hash)
	if err != nil || ok {
		t.Fatalf("verify wrong: ok=%v err=%v", ok, err)
	}
	hash2, err := hashPassword("correct horse battery staple", testParams)
	if err != nil {
		t.Fatalf("hash again: %v", err)
	}
	if hash == hash2 {
		t.Fatal("hashes must use unique salts")
	}
}

func TestVerifyPasswordMalformed(t *testing.T) {
	cases := []string{
		"",
		"plaintext",
		"$argon2i$v=19$m=8192,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=99999999,t=1,p=1$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=8192,t=1,p=1$not-base64!$aGFzaA",
		"$argon2id$v=19$m=8192,t=1,p=1$c2FsdA",
	}
	for _, encoded := range cases {
		if _, err := VerifyPassword("password", encoded); err == nil {
			t.Fatalf("expected error for %q", encoded)
		}
	}
}

func TestEqualizeTimingDoesNotPanic(t *testing.T) {
	EqualizeTiming("anything")
}

func TestGenerateTempPassword(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 20; i++ {
		password, err := GenerateTempPassword()
		if err != nil {
			t.Fatalf("generate: %v", err)
		}
		if len(password) != TempPasswordLength {
			t.Fatalf("length = %d, want %d", len(password), TempPasswordLength)
		}
		if err := ValidatePassword(password); err != nil {
			t.Fatalf("generated password failed policy: %v", err)
		}
		if strings.ContainsAny(password, "0O1lI") {
			t.Fatalf("generated password contains ambiguous characters: %q", password)
		}
		seen[password] = true
	}
	if len(seen) < 20 {
		t.Fatalf("generated passwords repeated: %d unique", len(seen))
	}
}
