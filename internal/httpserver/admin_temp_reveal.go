package httpserver

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

// adminTempRevealTTL bounds how long a generated temporary password may be
// fetched back after the POST redirect. The password is single-use: the first
// GET consumes it, so refreshing the result page cannot replay or re-reset.
const adminTempRevealTTL = 2 * time.Minute

type adminTempRevealEntry struct {
	actorID   int64
	password  string
	expiresAt time.Time
	storedAt  time.Time
}

type adminTempRevealStore struct {
	mu    sync.Mutex
	items map[string]adminTempRevealEntry
}

// adminTempReveals 是进程内一次性回显表。管理台单实例部署，密码本身在 DB
// 中只存 Argon2id hash；这里存活最多 2 分钟且读取即删除。
var adminTempReveals = &adminTempRevealStore{items: map[string]adminTempRevealEntry{}}

// put stores password for actorID and returns the opaque reveal token. An empty
// token means the caller must fall back to a plain notice without a password.
func (s *adminTempRevealStore) put(actorID int64, password string, expiresAt time.Time) string {
	if password == "" {
		return ""
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	token := hex.EncodeToString(raw)
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items == nil {
		s.items = map[string]adminTempRevealEntry{}
	}
	// Prune expired entries on write so the map stays bounded.
	for key, entry := range s.items {
		if now.Sub(entry.storedAt) > adminTempRevealTTL {
			delete(s.items, key)
		}
	}
	s.items[token] = adminTempRevealEntry{
		actorID:   actorID,
		password:  password,
		expiresAt: expiresAt,
		storedAt:  now,
	}
	return token
}

// take consumes token for actorID. Wrong actor or expired tokens are dropped
// without revealing the password.
func (s *adminTempRevealStore) take(token string, actorID int64) (string, time.Time, bool) {
	if token == "" {
		return "", time.Time{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.items[token]
	if !ok {
		return "", time.Time{}, false
	}
	delete(s.items, token)
	if entry.actorID != actorID || time.Since(entry.storedAt) > adminTempRevealTTL {
		return "", time.Time{}, false
	}
	return entry.password, entry.expiresAt, true
}
