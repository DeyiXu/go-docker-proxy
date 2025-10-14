package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// CacheItem 表示缓存的一项内容
type CacheItem struct {
	Data       []byte              // 实际的响应内容
	Headers    map[string][]string // HTTP 响应头
	StatusCode int                 // HTTP 状态码
	ExpiresAt  time.Time           // 过期时间
	CachedAt   time.Time           // 缓存时间
	Size       int64               // 内容大小
}

// CacheMetadata 用于持久化的元数据结构
type cacheMetadata struct {
	Headers     map[string][]string `json:"headers"`
	StatusCode  int                 `json:"statusCode"`
	ExpiresAt   int64               `json:"expiresAt"`
	CachedAt    int64               `json:"cachedAt"`
	Size        int64               `json:"size"`
	ContentType string              `json:"contentType"`
}

// CacheStats 缓存统计信息
type CacheStats struct {
	Hits        atomic.Int64
	Misses      atomic.Int64
	TotalSize   atomic.Int64
	ItemCount   atomic.Int64
	LastCleanup time.Time
}

// DockerRegistryCache 专为 Docker Registry 设计的缓存系统
type DockerRegistryCache struct {
	cacheDir    string
	manifestDir string // manifest 缓存目录
	blobDir     string // blob 缓存目录

	// 内存索引，快速查找
	index map[string]*CacheItem
	mutex sync.RWMutex

	// 统计信息
	stats      *CacheStats
	statsMutex sync.RWMutex

	// 配置
	maxSize         int64         // 最大缓存大小（字节）
	manifestTTL     time.Duration // manifest 缓存时间
	blobTTL         time.Duration // blob 缓存时间
	cleanupInterval time.Duration // 清理间隔
}

// NewDockerRegistryCache 创建专门的 Docker Registry 缓存
func NewDockerRegistryCache(cacheDir string) *DockerRegistryCache {
	manifestDir := filepath.Join(cacheDir, "manifests")
	blobDir := filepath.Join(cacheDir, "blobs")

	// 创建目录结构
	_ = os.MkdirAll(manifestDir, 0o755)
	_ = os.MkdirAll(blobDir, 0o755)

	cache := &DockerRegistryCache{
		cacheDir:        cacheDir,
		manifestDir:     manifestDir,
		blobDir:         blobDir,
		index:           make(map[string]*CacheItem),
		stats:           &CacheStats{},
		maxSize:         10 * 1024 * 1024 * 1024, // 默认 10GB
		manifestTTL:     1 * time.Hour,           // manifest 缓存 1 小时
		blobTTL:         7 * 24 * time.Hour,      // blob 缓存 7 天
		cleanupInterval: 30 * time.Minute,        // 每 30 分钟清理一次
	}

	// 启动后台清理协程
	go cache.cleanupLoop()

	return cache
}

// FileCache 为了兼容性保留的别名
type FileCache = DockerRegistryCache

// NewFileCache 为了兼容性保留的构造函数
func NewFileCache(cacheDir string) *FileCache {
	return NewDockerRegistryCache(cacheDir)
}

// Set 添加内容到缓存
func (c *DockerRegistryCache) Set(key string, data []byte, headers map[string][]string, statusCode int, ttl time.Duration) {
	now := time.Now()
	expiresAt := now.Add(ttl)

	// 根据路径类型决定 TTL（优先使用路径判断）
	if strings.Contains(key, "/manifests/") {
		expiresAt = now.Add(c.manifestTTL)
	} else if strings.Contains(key, "/blobs/") {
		expiresAt = now.Add(c.blobTTL)
	}

	item := &CacheItem{
		Data:       data,
		Headers:    headers,
		StatusCode: statusCode,
		ExpiresAt:  expiresAt,
		CachedAt:   now,
		Size:       int64(len(data)),
	}

	// 更新内存索引
	c.mutex.Lock()
	c.index[key] = item
	c.mutex.Unlock()

	// 更新统计
	c.stats.TotalSize.Add(item.Size)
	c.stats.ItemCount.Add(1)

	// 异步保存到磁盘
	go c.saveToFile(key, item)
}

// Get 从缓存获取内容
func (c *DockerRegistryCache) Get(key string) (*CacheItem, bool) {
	// 先查内存索引
	c.mutex.RLock()
	item, exists := c.index[key]
	c.mutex.RUnlock()

	if exists {
		// 检查是否过期
		if time.Now().Before(item.ExpiresAt) {
			c.stats.Hits.Add(1)
			return item, true
		}

		// 已过期，删除
		c.mutex.Lock()
		delete(c.index, key)
		c.mutex.Unlock()

		// 异步删除文件
		go c.deleteFile(key)
	}

	// 尝试从磁盘加载
	item, ok := c.loadFromFile(key)
	if ok {
		c.stats.Hits.Add(1)
		return item, true
	}

	c.stats.Misses.Add(1)
	return nil, false
}

// saveToFile 保存到磁盘
func (c *DockerRegistryCache) saveToFile(key string, item *CacheItem) {
	cacheKey := c.getCacheKey(key)
	dataPath := c.getFilePath(cacheKey, key)
	metaPath := dataPath + ".meta"

	dir := filepath.Dir(dataPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Printf("Failed to create cache directory %s: %v\n", dir, err)
		return
	}

	// 保存数据文件
	if err := os.WriteFile(dataPath, item.Data, 0o644); err != nil {
		fmt.Printf("Failed to save cache data %s: %v\n", dataPath, err)
		return
	}

	// 保存元数据
	contentType := ""
	if ct, ok := item.Headers["Content-Type"]; ok && len(ct) > 0 {
		contentType = ct[0]
	}

	meta := cacheMetadata{
		Headers:     item.Headers,
		StatusCode:  item.StatusCode,
		ExpiresAt:   item.ExpiresAt.Unix(),
		CachedAt:    item.CachedAt.Unix(),
		Size:        item.Size,
		ContentType: contentType,
	}

	metaBytes, err := json.Marshal(meta)
	if err != nil {
		fmt.Printf("Failed to marshal metadata for %s: %v\n", key, err)
		_ = os.Remove(dataPath)
		return
	}

	if err := os.WriteFile(metaPath, metaBytes, 0o644); err != nil {
		fmt.Printf("Failed to save cache metadata %s: %v\n", metaPath, err)
		_ = os.Remove(dataPath)
	}
}

// loadFromFile 从磁盘加载
func (c *DockerRegistryCache) loadFromFile(key string) (*CacheItem, bool) {
	cacheKey := c.getCacheKey(key)
	dataPath := c.getFilePath(cacheKey, key)
	metaPath := dataPath + ".meta"

	// 读取元数据
	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, false
	}

	var meta cacheMetadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		_ = os.Remove(dataPath)
		_ = os.Remove(metaPath)
		return nil, false
	}

	// 检查是否过期
	if time.Now().Unix() > meta.ExpiresAt {
		_ = os.Remove(dataPath)
		_ = os.Remove(metaPath)
		return nil, false
	}

	// 读取数据
	data, err := os.ReadFile(dataPath)
	if err != nil {
		_ = os.Remove(dataPath)
		_ = os.Remove(metaPath)
		return nil, false
	}

	item := &CacheItem{
		Data:       data,
		Headers:    meta.Headers,
		StatusCode: meta.StatusCode,
		ExpiresAt:  time.Unix(meta.ExpiresAt, 0),
		CachedAt:   time.Unix(meta.CachedAt, 0),
		Size:       meta.Size,
	}

	// 加载到内存索引
	c.mutex.Lock()
	c.index[key] = item
	c.mutex.Unlock()

	return item, true
}

// deleteFile 删除缓存文件
func (c *DockerRegistryCache) deleteFile(key string) {
	cacheKey := c.getCacheKey(key)
	dataPath := c.getFilePath(cacheKey, key)
	metaPath := dataPath + ".meta"

	_ = os.Remove(dataPath)
	_ = os.Remove(metaPath)

	c.stats.ItemCount.Add(-1)
}

// getCacheKey 生成缓存键（使用 SHA256）
func (c *DockerRegistryCache) getCacheKey(key string) string {
	hash := sha256.Sum256([]byte(key))
	return hex.EncodeToString(hash[:])
}

// getFilePath 获取文件路径（使用目录分层避免单个目录文件过多）
func (c *DockerRegistryCache) getFilePath(cacheKey string, originalKey string) string {
	// 根据内容类型决定基础目录
	var baseDir string
	if strings.Contains(originalKey, "/manifests/") {
		baseDir = c.manifestDir
	} else {
		baseDir = c.blobDir
	}

	// 使用前4个字符分层：前2个字符作为一级目录，3-4字符作为二级目录
	// 例如: abc123... -> ab/c1/abc123...
	return filepath.Join(baseDir, cacheKey[:2], cacheKey[2:4], cacheKey)
}

// cleanupLoop 后台清理循环
func (c *DockerRegistryCache) cleanupLoop() {
	ticker := time.NewTicker(c.cleanupInterval)
	defer ticker.Stop()

	for range ticker.C {
		c.cleanup()
	}
}

// cleanup 清理过期缓存
func (c *DockerRegistryCache) cleanup() {
	now := time.Now()
	toDelete := make([]string, 0)

	c.mutex.RLock()
	for key, item := range c.index {
		if now.After(item.ExpiresAt) {
			toDelete = append(toDelete, key)
		}
	}
	c.mutex.RUnlock()

	// 删除过期项
	for _, key := range toDelete {
		c.mutex.Lock()
		if item, exists := c.index[key]; exists {
			delete(c.index, key)
			c.stats.TotalSize.Add(-item.Size)
			c.stats.ItemCount.Add(-1)
		}
		c.mutex.Unlock()

		go c.deleteFile(key)
	}

	c.statsMutex.Lock()
	c.stats.LastCleanup = now
	c.statsMutex.Unlock()

	if len(toDelete) > 0 {
		fmt.Printf("Cleaned up %d expired cache items\n", len(toDelete))
	}
}

// GetStats 获取缓存统计信息
func (c *DockerRegistryCache) GetStats() CacheStats {
	c.statsMutex.RLock()
	defer c.statsMutex.RUnlock()

	return CacheStats{
		Hits:        c.stats.Hits,
		Misses:      c.stats.Misses,
		TotalSize:   c.stats.TotalSize,
		ItemCount:   c.stats.ItemCount,
		LastCleanup: c.stats.LastCleanup,
	}
}
