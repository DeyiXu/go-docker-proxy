package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// =============================================================================
// 接口定义 - 类似 distribution/distribution 的抽象层
// =============================================================================

// Descriptor 描述 blob 或 manifest 的元数据
type Descriptor struct {
	Digest    string `json:"digest"`    // SHA256 摘要
	Size      int64  `json:"size"`      // 内容大小
	MediaType string `json:"mediaType"` // 媒体类型
}

// CacheEntry 缓存条目
type CacheEntry struct {
	Descriptor Descriptor          `json:"descriptor"`
	Headers    map[string][]string `json:"headers"`
	StatusCode int                 `json:"statusCode"`
	Data       []byte              `json:"data,omitempty"`     // 小文件数据（内存缓存）
	BodyPath   string              `json:"bodyPath,omitempty"` // 大文件路径
	CachedAt   time.Time           `json:"cachedAt"`
	ExpiresAt  time.Time           `json:"expiresAt"`
}

// BlobStore 定义 blob 存储接口
type BlobStore interface {
	// Stat 检查 blob 是否存在，返回描述符
	Stat(ctx context.Context, digest string) (Descriptor, error)
	// Get 获取 blob 内容
	Get(ctx context.Context, digest string) (io.ReadCloser, error)
	// Put 存储 blob
	Put(ctx context.Context, digest string, content io.Reader, size int64) error
	// Delete 删除 blob
	Delete(ctx context.Context, digest string) error
}

// ManifestStore 定义 manifest 存储接口
type ManifestStore interface {
	// Get 获取 manifest（通过 tag 或 digest）
	Get(ctx context.Context, repo, reference string) (*CacheEntry, error)
	// Put 存储 manifest
	Put(ctx context.Context, repo, reference string, entry *CacheEntry) error
	// Delete 删除 manifest
	Delete(ctx context.Context, repo, reference string) error
}

// DescriptorCache 描述符缓存接口（内存层）
type DescriptorCache interface {
	Get(key string) (Descriptor, bool)
	Set(key string, desc Descriptor)
	Delete(key string)
}

// InflightResult 请求去重结果
type InflightResult struct {
	CacheKey string
	Cached   bool
	Error    error
}

// =============================================================================
// 统计信息
// =============================================================================

// CacheStatistics 缓存统计
type CacheStatistics struct {
	BlobHits       atomic.Int64
	BlobMisses     atomic.Int64
	ManifestHits   atomic.Int64
	ManifestMisses atomic.Int64
	TotalSize      atomic.Int64
	BlobCount      atomic.Int64
	ManifestCount  atomic.Int64
	Deduplication  atomic.Int64 // 请求去重次数
	LastCleanup    time.Time
}

// Snapshot 获取统计快照
func (s *CacheStatistics) Snapshot() map[string]interface{} {
	blobHits := s.BlobHits.Load()
	blobMisses := s.BlobMisses.Load()
	blobTotal := blobHits + blobMisses

	manifestHits := s.ManifestHits.Load()
	manifestMisses := s.ManifestMisses.Load()
	manifestTotal := manifestHits + manifestMisses

	blobHitRate := float64(0)
	if blobTotal > 0 {
		blobHitRate = float64(blobHits) / float64(blobTotal) * 100
	}

	manifestHitRate := float64(0)
	if manifestTotal > 0 {
		manifestHitRate = float64(manifestHits) / float64(manifestTotal) * 100
	}

	return map[string]interface{}{
		"blob": map[string]interface{}{
			"count":    s.BlobCount.Load(),
			"requests": blobTotal,
			"hits":     blobHits,
			"misses":   blobMisses,
			"hitRate":  fmt.Sprintf("%.2f%%", blobHitRate),
		},
		"manifest": map[string]interface{}{
			"count":    s.ManifestCount.Load(),
			"requests": manifestTotal,
			"hits":     manifestHits,
			"misses":   manifestMisses,
			"hitRate":  fmt.Sprintf("%.2f%%", manifestHitRate),
		},
		"totalSize":      s.TotalSize.Load(),
		"totalSizeHuman": formatBytes(s.TotalSize.Load()),
		"deduplication":  s.Deduplication.Load(),
		"lastCleanup":    s.LastCleanup.Format(time.RFC3339),
	}
}

// =============================================================================
// 缓存管理器 - 统一入口
// =============================================================================

// CacheConfig 缓存配置
type CacheConfig struct {
	Dir             string        // 缓存目录
	MaxSize         int64         // 最大缓存大小（字节）
	ManifestTTL     time.Duration // manifest by tag 过期时间
	BlobTTL         time.Duration // blob 过期时间（不可变内容）
	CleanupInterval time.Duration // 清理间隔
	Debug           bool          // 调试模式
}

// DefaultCacheConfig 默认配置
func DefaultCacheConfig() *CacheConfig {
	return &CacheConfig{
		Dir:             "./cache",
		MaxSize:         10 * 1024 * 1024 * 1024, // 10GB
		ManifestTTL:     24 * time.Hour,
		BlobTTL:         365 * 24 * time.Hour, // 1年
		CleanupInterval: 30 * time.Minute,
		Debug:           false,
	}
}

// CacheManager 缓存管理器
type CacheManager struct {
	config *CacheConfig

	// 存储层
	blobStore     *FileBlobStore
	manifestStore *FileManifestStore

	// 内存缓存层
	descriptorCache *LRUDescriptorCache

	// 请求去重
	inflight *InflightManager

	// 统计
	stats *CacheStatistics

	// 控制
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewCacheManager 创建缓存管理器
func NewCacheManager(config *CacheConfig) (*CacheManager, error) {
	if config == nil {
		config = DefaultCacheConfig()
	}

	// 创建目录结构
	dirs := []string{
		config.Dir,
		filepath.Join(config.Dir, "blobs"),
		filepath.Join(config.Dir, "manifests"),
		filepath.Join(config.Dir, "tmp"),
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("failed to create cache directory %s: %w", dir, err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	cm := &CacheManager{
		config:          config,
		blobStore:       NewFileBlobStore(filepath.Join(config.Dir, "blobs"), config.BlobTTL),
		manifestStore:   NewFileManifestStore(filepath.Join(config.Dir, "manifests"), config.ManifestTTL, config.BlobTTL),
		descriptorCache: NewLRUDescriptorCache(10000),
		inflight:        NewInflightManager(),
		stats:           &CacheStatistics{},
		ctx:             ctx,
		cancel:          cancel,
	}

	// 启动后台清理
	cm.wg.Add(1)
	go cm.cleanupLoop()

	// 启动时加载索引
	go cm.loadIndex()

	return cm, nil
}

// Close 关闭缓存管理器
func (cm *CacheManager) Close() error {
	cm.cancel()
	cm.wg.Wait()
	return nil
}

// =============================================================================
// 核心缓存操作
// =============================================================================

// GetBlob 获取 blob
func (cm *CacheManager) GetBlob(ctx context.Context, cacheKey, digest string) (*CacheEntry, io.ReadCloser, error) {
	// 1. 先检查描述符缓存
	if desc, ok := cm.descriptorCache.Get(digest); ok {
		// 尝试从存储获取内容
		reader, err := cm.blobStore.Get(ctx, digest)
		if err == nil {
			cm.stats.BlobHits.Add(1)
			return &CacheEntry{
				Descriptor: desc,
				StatusCode: http.StatusOK,
			}, reader, nil
		}
		// 描述符存在但文件不存在，删除描述符
		cm.descriptorCache.Delete(digest)
	}

	// 2. 直接检查存储
	desc, err := cm.blobStore.Stat(ctx, digest)
	if err == nil {
		reader, err := cm.blobStore.Get(ctx, digest)
		if err == nil {
			cm.stats.BlobHits.Add(1)
			cm.descriptorCache.Set(digest, desc)
			return &CacheEntry{
				Descriptor: desc,
				StatusCode: http.StatusOK,
			}, reader, nil
		}
	}

	cm.stats.BlobMisses.Add(1)
	return nil, nil, ErrNotFound
}

// PutBlob 存储 blob
func (cm *CacheManager) PutBlob(ctx context.Context, cacheKey, digest string, content io.Reader, size int64, headers map[string][]string) error {
	// 存储内容
	if err := cm.blobStore.Put(ctx, digest, content, size); err != nil {
		return err
	}

	// 更新描述符缓存
	mediaType := ""
	if ct, ok := headers["Content-Type"]; ok && len(ct) > 0 {
		mediaType = ct[0]
	}
	desc := Descriptor{
		Digest:    digest,
		Size:      size,
		MediaType: mediaType,
	}
	cm.descriptorCache.Set(digest, desc)

	cm.stats.BlobCount.Add(1)
	cm.stats.TotalSize.Add(size)

	return nil
}

// GetManifest 获取 manifest
func (cm *CacheManager) GetManifest(ctx context.Context, repo, reference string) (*CacheEntry, error) {
	entry, err := cm.manifestStore.Get(ctx, repo, reference)
	if err != nil {
		cm.stats.ManifestMisses.Add(1)
		return nil, err
	}

	cm.stats.ManifestHits.Add(1)
	return entry, nil
}

// PutManifest 存储 manifest
func (cm *CacheManager) PutManifest(ctx context.Context, repo, reference string, data []byte, headers map[string][]string, statusCode int) error {
	mediaType := ""
	if ct, ok := headers["Content-Type"]; ok && len(ct) > 0 {
		mediaType = ct[0]
	}

	// 计算摘要
	hash := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(hash[:])

	entry := &CacheEntry{
		Descriptor: Descriptor{
			Digest:    digest,
			Size:      int64(len(data)),
			MediaType: mediaType,
		},
		Headers:    headers,
		StatusCode: statusCode,
		CachedAt:   time.Now(),
	}

	// 根据引用类型设置过期时间
	if strings.HasPrefix(reference, "sha256:") {
		// digest 引用，内容不可变
		entry.ExpiresAt = time.Now().Add(cm.config.BlobTTL)
	} else {
		// tag 引用，可能会更新
		entry.ExpiresAt = time.Now().Add(cm.config.ManifestTTL)
	}

	if err := cm.manifestStore.Put(ctx, repo, reference, entry); err != nil {
		return err
	}

	cm.stats.ManifestCount.Add(1)
	cm.stats.TotalSize.Add(int64(len(data)))

	return nil
}

// =============================================================================
// 请求去重
// =============================================================================

// TryInflight 尝试加入 inflight 请求
// 返回: isFirst, waitFunc, doneFunc
func (cm *CacheManager) TryInflight(key string) (bool, func(context.Context) (*InflightResult, error), func(*InflightResult)) {
	isFirst, waitFn, doneFn := cm.inflight.TryStart(key)

	if !isFirst {
		// 包装 wait 函数，返回 InflightResult
		wrappedWait := func(ctx context.Context) (*InflightResult, error) {
			err := waitFn(ctx)
			if err != nil {
				return nil, err
			}
			// 返回成功结果，调用者需要检查缓存
			return &InflightResult{CacheKey: key, Cached: true}, nil
		}
		return false, wrappedWait, nil
	}

	// 包装 done 函数
	wrappedDone := func(result *InflightResult) {
		if result != nil && !result.Cached {
			doneFn(ErrNotFound)
		} else {
			doneFn(nil)
		}
	}

	return true, nil, wrappedDone
}

// =============================================================================
// 简化的 HTTP 缓存接口
// =============================================================================

// Get 获取缓存条目（统一接口）
// 注意：对于 blob 类型，建议使用 GetBlobReader 进行流式传输
func (cm *CacheManager) Get(cacheKey string) (*CacheEntry, bool) {
	pathType, repo, reference := ParsePath(cacheKey)

	ctx := context.Background()

	switch pathType {
	case "manifest":
		entry, err := cm.GetManifest(ctx, repo, reference)
		if err == nil && entry != nil {
			return entry, true
		}
		// GetManifest 内部已经记录了 miss
	case "blob":
		// 对于 blob，仅返回元数据（检查是否存在）
		// 实际数据通过 GetBlobReader 流式读取
		digest := GetDigestFromPath(cacheKey)
		if digest != "" {
			desc, err := cm.blobStore.Stat(ctx, digest)
			if err == nil {
				cm.stats.BlobHits.Add(1)
				entry := &CacheEntry{
					Descriptor: desc,
					StatusCode: http.StatusOK,
				}
				cm.setBlobHeaders(entry)
				return entry, true
			}
			cm.stats.BlobMisses.Add(1)
		}
	}

	return nil, false
}

// setBlobHeaders 设置 blob 响应的标准 headers
func (cm *CacheManager) setBlobHeaders(entry *CacheEntry) {
	if entry.Headers == nil {
		entry.Headers = make(map[string][]string)
	}
	entry.Headers["Content-Length"] = []string{strconv.FormatInt(entry.Descriptor.Size, 10)}
	if entry.Descriptor.MediaType != "" {
		entry.Headers["Content-Type"] = []string{entry.Descriptor.MediaType}
	} else {
		entry.Headers["Content-Type"] = []string{"application/octet-stream"}
	}
	entry.Headers["Docker-Content-Digest"] = []string{entry.Descriptor.Digest}
}

// GetBlobReader 获取 blob 的流式 reader（用于大文件流式传输）
func (cm *CacheManager) GetBlobReader(cacheKey string) (*CacheEntry, io.ReadCloser, bool) {
	digest := GetDigestFromPath(cacheKey)
	if digest == "" {
		return nil, nil, false
	}

	ctx := context.Background()
	entry, reader, err := cm.GetBlob(ctx, cacheKey, digest)
	if err != nil || entry == nil {
		return nil, nil, false
	}

	cm.setBlobHeaders(entry)
	return entry, reader, true
}

// Put 存储缓存条目（统一接口）
func (cm *CacheManager) Put(cacheKey string, entry *CacheEntry) error {
	pathType, repo, reference := ParsePath(cacheKey)

	ctx := context.Background()

	switch pathType {
	case "manifest":
		// Manifest 存储需要数据
		return cm.manifestStore.Put(ctx, repo, reference, entry)
	case "blob":
		// Blob 存储：写入实际数据到文件存储
		digest := GetDigestFromPath(cacheKey)
		if digest != "" && len(entry.Data) > 0 {
			// 使用 bytes.NewReader 创建 io.Reader
			reader := bytes.NewReader(entry.Data)
			if err := cm.PutBlob(ctx, cacheKey, digest, reader, int64(len(entry.Data)), entry.Headers); err != nil {
				return err
			}
		} else if digest != "" {
			// 仅更新描述符缓存（无数据时）
			cm.descriptorCache.Set(digest, entry.Descriptor)
		}
	}

	return nil
}

// =============================================================================
// HTTP 集成辅助方法
// =============================================================================

// CacheKey 生成缓存键
func CacheKey(host, path string) string {
	return host + path
}

// ParsePath 解析路径，提取 repo 和 reference
// 路径格式: host/v2/{repo}/manifests/{reference} 或 /v2/{repo}/blobs/{digest}
func ParsePath(path string) (pathType, repo, reference string) {
	// 找到 /v2/ 的位置（cacheKey 可能包含 host 前缀）
	idx := strings.Index(path, "/v2/")
	if idx == -1 {
		return "", "", ""
	}
	path = path[idx:] // 只保留 /v2/ 及之后的部分

	parts := strings.Split(strings.TrimPrefix(path, "/v2/"), "/")
	if len(parts) < 3 {
		return "", "", ""
	}

	// 找到 manifests 或 blobs 的位置
	for i, part := range parts {
		if part == "manifests" && i+1 < len(parts) {
			repo = strings.Join(parts[:i], "/")
			reference = strings.Join(parts[i+1:], "/")
			return "manifest", repo, reference
		}
		if part == "blobs" && i+1 < len(parts) {
			repo = strings.Join(parts[:i], "/")
			reference = strings.Join(parts[i+1:], "/")
			return "blob", repo, reference
		}
	}

	return "", "", ""
}

// IsCacheable 判断路径是否可缓存
func IsCacheable(path string) bool {
	return strings.Contains(path, "/manifests/") || strings.Contains(path, "/blobs/sha256:")
}

// GetDigestFromPath 从路径提取 digest
func GetDigestFromPath(path string) string {
	if idx := strings.Index(path, "sha256:"); idx != -1 {
		end := idx + 71 // sha256: + 64 hex chars
		if end <= len(path) {
			return path[idx:end]
		}
	}
	return ""
}

// =============================================================================
// 后台任务
// =============================================================================

func (cm *CacheManager) cleanupLoop() {
	defer cm.wg.Done()

	ticker := time.NewTicker(cm.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-cm.ctx.Done():
			return
		case <-ticker.C:
			cm.cleanup()
		}
	}
}

func (cm *CacheManager) cleanup() {
	now := time.Now()

	// 清理 manifest
	cleaned := cm.manifestStore.Cleanup()

	// 清理 blob（基于 LRU 和大小限制）
	cleaned += cm.blobStore.Cleanup(cm.config.MaxSize)

	cm.stats.LastCleanup = now

	if cleaned > 0 && cm.config.Debug {
		log.Printf("[Cache] Cleaned up %d expired items", cleaned)
	}
}

func (cm *CacheManager) loadIndex() {
	// 扫描现有缓存文件，建立索引
	// 这是一个可选的优化，可以在启动时预热缓存
	if cm.config.Debug {
		log.Printf("[Cache] Loading cache index from %s", cm.config.Dir)
	}

	blobCount, manifestCount, totalSize := cm.blobStore.LoadIndex()
	manifestCount2, manifestSize := cm.manifestStore.LoadIndex()

	cm.stats.BlobCount.Store(blobCount)
	cm.stats.ManifestCount.Store(manifestCount + manifestCount2)
	cm.stats.TotalSize.Store(totalSize + manifestSize)

	if cm.config.Debug {
		log.Printf("[Cache] Loaded index: %d blobs, %d manifests, %s total",
			blobCount, manifestCount+manifestCount2, formatBytes(totalSize+manifestSize))
	}
}

// Stats 获取统计信息
func (cm *CacheManager) Stats() map[string]interface{} {
	stats := cm.stats.Snapshot()
	stats["inflight"] = cm.inflight.Stats()
	return stats
}

// =============================================================================
// 错误定义
// =============================================================================

var (
	ErrNotFound = fmt.Errorf("not found in cache")
	ErrExpired  = fmt.Errorf("cache entry expired")
)

// =============================================================================
// 辅助函数
// =============================================================================

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
