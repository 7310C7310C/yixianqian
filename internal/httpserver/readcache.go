package httpserver

import (
	"container/list"
	"strconv"
	"strings"
	"sync"
	"time"
)

// readCache 是「用户 × query × viewRole」维度的短 TTL 进程内缓存，用于
// /legacy/api/home 与 /legacy/api/cards 的**成功响应**：
//
//   - 只缓存成功序列化后的 JSON 字节，错误/未授权/重定向一律不进缓存；
//   - key 含 userID，跨用户不可能命中（有单测锁定）；
//   - TTL 10s（批次9 决策）：写路径主动失效 + 最多 10s 陈旧，换掉 p95 尾延迟与
//     home 每次 15 条 SQL；不改变响应头（仍是 no-store、Vary: Cookie）；
//   - 容量有界（LRU，默认 4096 条），进程内单实例，不跨实例共享。
type readCache struct {
	ttl      time.Duration
	maxItems int

	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List
}

type readCacheEntry struct {
	key     string
	body    []byte
	expires time.Time
}

func newReadCache(ttl time.Duration, maxItems int) *readCache {
	if ttl <= 0 || maxItems <= 0 {
		return nil
	}
	return &readCache{ttl: ttl, maxItems: maxItems, entries: make(map[string]*list.Element), order: list.New()}
}

// readCacheKey 统一 key 形状：userID|kind|viewRole|rawQuery。
func readCacheKey(userID int64, kind, viewRole, rawQuery string) string {
	return strconv.FormatInt(userID, 10) + "|" + kind + "|" + viewRole + "|" + rawQuery
}

// Get 命中且未过期才返回；过期条目顺手删除。
func (c *readCache) Get(key string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return nil, false
	}
	entry := el.Value.(*readCacheEntry)
	if time.Now().After(entry.expires) {
		c.removeLocked(el)
		return nil, false
	}
	c.order.MoveToFront(el)
	return entry.body, true
}

// Put 写入/刷新一条成功响应；超过容量时淘汰最久未用。
func (c *readCache) Put(key string, body []byte) {
	if c == nil || len(body) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	expires := time.Now().Add(c.ttl)
	if el, ok := c.entries[key]; ok {
		entry := el.Value.(*readCacheEntry)
		entry.body = body
		entry.expires = expires
		c.order.MoveToFront(el)
		return
	}
	el := c.order.PushFront(&readCacheEntry{key: key, body: body, expires: expires})
	c.entries[key] = el
	for c.order.Len() > c.maxItems {
		if tail := c.order.Back(); tail != nil {
			c.removeLocked(tail)
		}
	}
}

// InvalidateUser 清掉某用户所有 kind/query 变体（写路径调用）。
func (c *readCache) InvalidateUser(userID int64) {
	if c == nil {
		return
	}
	prefix := strconv.FormatInt(userID, 10) + "|"
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, el := range c.entries {
		if strings.HasPrefix(key, prefix) {
			c.removeLocked(el)
		}
	}
}

func (c *readCache) removeLocked(el *list.Element) {
	entry := el.Value.(*readCacheEntry)
	delete(c.entries, entry.key)
	c.order.Remove(el)
}

// Len 供测试/诊断。
func (c *readCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
