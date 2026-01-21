package main

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
)

// =============================================================================
// LRU Descriptor Cache - 内存层描述符缓存
// =============================================================================

// LRUDescriptorCache 使用 LRU 算法的描述符缓存
type LRUDescriptorCache struct {
	cache *expirable.LRU[string, Descriptor]
	mu    sync.RWMutex

	hits   atomic.Int64
	misses atomic.Int64
}

// NewLRUDescriptorCache 创建 LRU 描述符缓存
func NewLRUDescriptorCache(maxSize int) *LRUDescriptorCache {
	if maxSize <= 0 {
		maxSize = 10000
	}

	cache := expirable.NewLRU[string, Descriptor](maxSize, nil, 24*time.Hour)

	return &LRUDescriptorCache{
		cache: cache,
	}
}

// Get 获取描述符
func (c *LRUDescriptorCache) Get(key string) (Descriptor, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	desc, ok := c.cache.Get(key)
	if ok {
		c.hits.Add(1)
		return desc, true
	}
	c.misses.Add(1)
	return Descriptor{}, false
}

// Set 设置描述符
func (c *LRUDescriptorCache) Set(key string, desc Descriptor) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache.Add(key, desc)
}

// Delete 删除描述符
func (c *LRUDescriptorCache) Delete(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache.Remove(key)
}

// Stats 获取统计信息
func (c *LRUDescriptorCache) Stats() map[string]interface{} {
	c.mu.RLock()
	defer c.mu.RUnlock()

	hits := c.hits.Load()
	misses := c.misses.Load()
	total := hits + misses
	hitRate := float64(0)
	if total > 0 {
		hitRate = float64(hits) / float64(total) * 100
	}

	return map[string]interface{}{
		"size":    c.cache.Len(),
		"hits":    hits,
		"misses":  misses,
		"hitRate": hitRate,
	}
}

// =============================================================================
// Inflight Manager - 请求去重
// =============================================================================

// InflightManager 管理正在进行的请求，防止重复请求上游
type InflightManager struct {
	mu       sync.Mutex
	inflight map[string]*inflightEntry

	totalRequests atomic.Int64
	deduplicated  atomic.Int64
}

type inflightEntry struct {
	done     chan struct{}
	err      error
	watchers int
	started  time.Time
}

// NewInflightManager 创建请求去重管理器
func NewInflightManager() *InflightManager {
	return &InflightManager{
		inflight: make(map[string]*inflightEntry),
	}
}

// TryStart 尝试开始一个请求
// 返回值：
//   - isFirst: 是否是第一个请求者
//   - wait: 等待函数（非第一个请求者使用）
//   - done: 完成函数（第一个请求者完成后调用）
func (m *InflightManager) TryStart(key string) (isFirst bool, wait func(ctx context.Context) error, done func(err error)) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.totalRequests.Add(1)

	// 检查是否已有请求
	if entry, exists := m.inflight[key]; exists {
		entry.watchers++
		m.deduplicated.Add(1)

		return false, func(ctx context.Context) error {
			select {
			case <-entry.done:
				// 成功完成，减少 watchers 计数
				m.mu.Lock()
				entry.watchers--
				m.mu.Unlock()
				return entry.err
			case <-ctx.Done():
				m.mu.Lock()
				entry.watchers--
				m.mu.Unlock()
				return ctx.Err()
			}
		}, nil
	}

	// 创建新条目
	entry := &inflightEntry{
		done:     make(chan struct{}),
		watchers: 0,
		started:  time.Now(),
	}
	m.inflight[key] = entry

	return true, nil, func(err error) {
		m.mu.Lock()
		defer m.mu.Unlock()

		entry.err = err
		close(entry.done)
		delete(m.inflight, key)
	}
}

// Stats 获取统计信息
func (m *InflightManager) Stats() map[string]interface{} {
	m.mu.Lock()
	activeKeys := make([]string, 0, len(m.inflight))
	for key := range m.inflight {
		activeKeys = append(activeKeys, key)
	}
	m.mu.Unlock()

	totalReqs := m.totalRequests.Load()
	dedup := m.deduplicated.Load()

	savingsRate := float64(0)
	if totalReqs > 0 {
		savingsRate = float64(dedup) / float64(totalReqs) * 100
	}

	return map[string]interface{}{
		"totalRequests": totalReqs,
		"deduplicated":  dedup,
		"savingsRate":   savingsRate,
		"currentActive": len(m.inflight),
		"activeKeys":    activeKeys,
	}
}
