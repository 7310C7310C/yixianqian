package httpserver

import (
	"container/list"
	"sync"
	"sync/atomic"
	"time"
)

// readCache 是 /legacy/api/home 与 /legacy/api/cards 成功响应的进程内短缓存。
//
// 设计（批次9 复审后）：
//   - 键含**全局世代号 epoch**：任何成功的非 GET `/legacy/api/*` 写请求都会 epoch++，
//     写后所有用户的旧条目立即不可达（不需要逐用户失效，不会漏）；GET/错误不 bump；
//   - 双重上限：总字节（READ_CACHE_BYTES，默认 32MiB）+ 单条上限（256KiB，超过不缓存）
//   - 条数（READ_CACHE_ITEMS，默认 1024）；按**字节 LRU** 淘汰，最坏内存 ≤ 总字节上限；
//   - 过期条目在 Get 时删除，且 Put 时按时间片顺带清理（sweep）；
//   - 三项配置任一为 0 → 完全关闭（kill switch，不回落默认值）；
//   - 仅单实例语义：多实例需 pub/sub 传播 epoch（文档记录，不在本批）。
type readCache struct {
	ttl       time.Duration
	maxItems  int
	maxBytes  int64
	maxEntry  int64
	epoch     atomic.Int64
	lastSweep atomic.Int64

	mu      sync.Mutex
	entries map[string]*list.Element
	order   *list.List
	bytes   int64
}

type readCacheEntry struct {
	key     string
	body    []byte
	expires time.Time
	// cost = 键长 + body 长（键也占用内存预算）
	cost int64
}

const (
	readCacheMaxEntryDefault = 256 << 10
	readCacheSweepInterval   = 30 * time.Second
)

func newReadCache(enabled bool, ttl time.Duration, maxItems int, maxBytes, maxEntry int64) *readCache {
	if !enabled || ttl <= 0 || maxItems <= 0 || maxBytes <= 0 {
		return nil // kill switch：ENABLED=false 或任一上限为 0 都完全关闭（零值 Config 同样关闭）
	}
	if maxEntry <= 0 || maxEntry > maxBytes {
		maxEntry = readCacheMaxEntryDefault
		if maxEntry > maxBytes {
			maxEntry = maxBytes
		}
	}
	return &readCache{
		ttl:      ttl,
		maxItems: maxItems,
		maxBytes: maxBytes,
		maxEntry: maxEntry,
		entries:  make(map[string]*list.Element),
		order:    list.New(),
	}
}

// key 由调用方给出业务部分；epoch 由缓存内部加在最前面，Bump 后旧 key 自然不可达。
func (c *readCache) key(parts string) string {
	return parts + "@" + strconvEpoch(c.epoch.Load())
}

func strconvEpoch(v int64) string {
	// 避免引入 strconv 只为一个数字（热路径小优化意义不大，保持可读）。
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	n := v
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// readCacheParts 是业务键：userID|kind|viewRole|rawQuery（epoch 由缓存内部追加）。
func readCacheParts(userID int64, kind, viewRole, rawQuery string) string {
	return strconvEpoch(userID) + "|" + kind + "|" + viewRole + "|" + rawQuery
}

// BumpEpoch 让所有既有条目失效（成功的非 GET 写请求后调用，O(1)）。
func (c *readCache) BumpEpoch() {
	if c == nil {
		return
	}
	c.epoch.Add(1)
}

// Get 命中且未过期才返回；过期条目顺手删除。
func (c *readCache) Get(parts string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	key := c.key(parts)
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

// Put 写入/刷新一条成功响应；键长+body 都计入字节预算，超单条上限不缓存。
func (c *readCache) Put(parts string, body []byte) {
	c.put(parts, body, -1)
}

// PutIfEpoch 是 CAS 写入：若构建期间 epoch 已变化（有并发写），丢弃这次回填，
// 避免把「写前」的旧数据写进新一代缓存。
func (c *readCache) PutIfEpoch(parts string, body []byte, epoch int64) bool {
	return c.put(parts, body, epoch)
}

func (c *readCache) put(parts string, body []byte, expectEpoch int64) bool {
	if c == nil || len(body) == 0 || int64(len(body)) > c.maxEntry {
		return false
	}
	key := c.key(parts)
	if expectEpoch >= 0 && c.epoch.Load() != expectEpoch {
		return false
	}
	cost := int64(len(key) + len(body))
	if cost > c.maxBytes {
		return false
	}
	now := time.Now()
	expires := now.Add(c.ttl)
	c.mu.Lock()
	defer c.mu.Unlock()
	if expectEpoch >= 0 && c.epoch.Load() != expectEpoch {
		return false
	}
	if el, ok := c.entries[key]; ok {
		entry := el.Value.(*readCacheEntry)
		c.bytes += cost - entry.cost
		entry.body = body
		entry.cost = cost
		entry.expires = expires
		c.order.MoveToFront(el)
	} else {
		el := c.order.PushFront(&readCacheEntry{key: key, body: body, expires: expires, cost: cost})
		c.entries[key] = el
		c.bytes += cost
	}
	for c.order.Len() > c.maxItems || c.bytes > c.maxBytes {
		tail := c.order.Back()
		if tail == nil {
			break
		}
		c.removeLocked(tail)
	}
	c.sweepLocked(now)
	return true
}

// sweepLocked 每 30s 最多清一次过期条目，避免长期只有 Put 的场景堆积。
func (c *readCache) sweepLocked(now time.Time) {
	last := c.lastSweep.Load()
	if last != 0 && now.Sub(time.Unix(0, last)) < readCacheSweepInterval {
		return
	}
	c.lastSweep.Store(now.UnixNano())
	for el := c.order.Back(); el != nil; {
		prev := el.Prev()
		entry := el.Value.(*readCacheEntry)
		if now.After(entry.expires) {
			c.removeLocked(el)
		}
		el = prev
	}
}

func (c *readCache) removeLocked(el *list.Element) {
	entry := el.Value.(*readCacheEntry)
	delete(c.entries, entry.key)
	c.order.Remove(el)
	c.bytes -= entry.cost
}

// Len/Bytes 供测试与诊断。
func (c *readCache) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

func (c *readCache) Bytes() int64 {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bytes
}

// Epoch 供测试断言。
func (c *readCache) Epoch() int64 {
	if c == nil {
		return 0
	}
	return c.epoch.Load()
}
