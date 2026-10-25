// 缩略图磁盘缓存：只缓存 ?size=thumb 的 JPEG 字节（几十 KB），原图永不进缓存。
// 授权在调用方完成，缓存层不参与鉴权。
//
// 一致性：
//   - 缓存 key 由 mediaID + object key + size 派生：替换缩略图会换 object key，
//     旧条目自然失效；
//   - 媒体删除时调用 RemoveMedia(mediaID) 主动清理该媒体的所有变体；
//   - 缓存文件丢失/损坏按 miss 处理，不影响请求。
package media

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ThumbCache 是有界 LRU 磁盘缓存（按总字节数淘汰，不是条数）。
type ThumbCache struct {
	dir   string
	limit int64

	mu      sync.Mutex
	entries map[string]*thumbEntry
	total   int64
}

type thumbEntry struct {
	size  int64
	atime time.Time
}

const thumbCacheSuffix = ".bin"

// NewThumbCache 打开（必要时创建）缓存目录并扫描已有条目。dir 为空返回 nil，
// 表示关闭缓存（测试与未配置环境走原路径）。
func NewThumbCache(dir string, limit int64) (*ThumbCache, error) {
	if strings.TrimSpace(dir) == "" || limit <= 0 {
		return nil, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("media: thumb cache dir: %w", err)
	}
	c := &ThumbCache{dir: dir, limit: limit, entries: make(map[string]*thumbEntry)}
	if err := c.load(); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *ThumbCache) load() error {
	dirEntries, err := os.ReadDir(c.dir)
	if err != nil {
		return fmt.Errorf("media: scan thumb cache: %w", err)
	}
	now := time.Now()
	for _, de := range dirEntries {
		name := de.Name()
		if de.IsDir() || !strings.HasSuffix(name, thumbCacheSuffix) {
			continue
		}
		info, statErr := de.Info()
		if statErr != nil {
			continue
		}
		key := strings.TrimSuffix(name, thumbCacheSuffix)
		c.entries[key] = &thumbEntry{size: info.Size(), atime: now}
		c.total += info.Size()
	}
	// 启动时把目录收敛到上限，避免上次写满后一直超限。
	c.evictLocked("")
	return nil
}

// ThumbCacheKey 生成缓存 key：媒体 id + 缩略图对象 key + 字节数。
// 对象 key 变化（替换照片）或尺寸变化都会得到新 key。
func ThumbCacheKey(mediaID, objectKey string, size int64) string {
	sum := sha256.Sum256([]byte(objectKey + "|" + strconv.FormatInt(size, 10)))
	return mediaID + "__" + hex.EncodeToString(sum[:8])
}

func (c *ThumbCache) path(key string) string {
	return filepath.Join(c.dir, key+thumbCacheSuffix)
}

// Get 返回缓存字节；文件缺失或读失败都按 miss 处理并清掉索引。
func (c *ThumbCache) Get(key string) ([]byte, bool) {
	if c == nil || key == "" {
		return nil, false
	}
	c.mu.Lock()
	entry, ok := c.entries[key]
	if ok {
		entry.atime = time.Now()
	}
	c.mu.Unlock()
	if !ok {
		return nil, false
	}
	data, err := os.ReadFile(c.path(key))
	if err != nil {
		c.mu.Lock()
		if cur, ok := c.entries[key]; ok && cur == entry {
			delete(c.entries, key)
			c.total -= cur.size
		}
		c.mu.Unlock()
		return nil, false
	}
	return data, true
}

// Put 写入一条缓存（先写临时文件再 rename，避免读到半截文件）。
func (c *ThumbCache) Put(key string, data []byte) error {
	if c == nil || key == "" || len(data) == 0 {
		return nil
	}
	size := int64(len(data))
	if size > c.limit {
		return nil // 单条就超过整个缓存：不缓存
	}
	dir := c.dir
	tmp, err := os.CreateTemp(dir, "tmp-*")
	if err != nil {
		return fmt.Errorf("media: thumb cache temp: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("media: thumb cache write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("media: thumb cache close: %w", err)
	}
	if err := os.Rename(tmpName, c.path(key)); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("media: thumb cache rename: %w", err)
	}
	c.mu.Lock()
	if old, ok := c.entries[key]; ok {
		c.total -= old.size
	}
	c.entries[key] = &thumbEntry{size: size, atime: time.Now()}
	c.total += size
	c.evictLocked(key)
	c.mu.Unlock()
	return nil
}

// evictLocked 按最久未用淘汰，直到总量回到上限内。keep 是刚写入的 key，
// 允许它在临界情况下留下（否则刚写就删没有意义）。
func (c *ThumbCache) evictLocked(keep string) {
	if c.total <= c.limit {
		return
	}
	type kv struct {
		key string
		at  time.Time
	}
	candidates := make([]kv, 0, len(c.entries))
	for key, entry := range c.entries {
		if key == keep {
			continue
		}
		candidates = append(candidates, kv{key: key, at: entry.atime})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].at.Before(candidates[j].at) })
	for _, cand := range candidates {
		if c.total <= c.limit {
			return
		}
		entry := c.entries[cand.key]
		if entry == nil {
			continue
		}
		delete(c.entries, cand.key)
		c.total -= entry.size
		_ = os.Remove(c.path(cand.key))
	}
}

// RemoveMedia 删除某媒体的所有缩略图变体（媒体删除时调用）。
func (c *ThumbCache) RemoveMedia(mediaID string) {
	if c == nil || mediaID == "" {
		return
	}
	prefix := mediaID + "__"
	c.mu.Lock()
	var doomed []string
	for key, entry := range c.entries {
		if strings.HasPrefix(key, prefix) {
			doomed = append(doomed, key)
			c.total -= entry.size
			delete(c.entries, key)
		}
	}
	c.mu.Unlock()
	for _, key := range doomed {
		_ = os.Remove(c.path(key))
	}
}

// Stats 供测试与预热命令报告使用。
func (c *ThumbCache) Stats() (entries int, bytes int64) {
	if c == nil {
		return 0, 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.total
}

// Dir 返回缓存目录（预热命令与运维排查用）。
func (c *ThumbCache) Dir() string { return c.dir }
