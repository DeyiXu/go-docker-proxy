package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// =============================================================================
// FileBlobStore - 文件系统 Blob 存储
// =============================================================================

// FileBlobStore 基于文件系统的 blob 存储
type FileBlobStore struct {
	dir string
	ttl time.Duration

	mu    sync.RWMutex
	index map[string]*blobMeta // digest -> metadata
}

type blobMeta struct {
	Digest    string    `json:"digest"`
	Size      int64     `json:"size"`
	MediaType string    `json:"mediaType"`
	CachedAt  time.Time `json:"cachedAt"`
	ExpiresAt time.Time `json:"expiresAt"`
	FilePath  string    `json:"filePath"`
}

// NewFileBlobStore 创建 blob 存储
func NewFileBlobStore(dir string, ttl time.Duration) *FileBlobStore {
	return &FileBlobStore{
		dir:   dir,
		ttl:   ttl,
		index: make(map[string]*blobMeta),
	}
}

// Stat 检查 blob 是否存在
func (s *FileBlobStore) Stat(ctx context.Context, digest string) (Descriptor, error) {
	s.mu.RLock()
	meta, ok := s.index[digest]
	s.mu.RUnlock()

	if ok && time.Now().Before(meta.ExpiresAt) {
		return Descriptor{
			Digest:    meta.Digest,
			Size:      meta.Size,
			MediaType: meta.MediaType,
		}, nil
	}

	// 尝试从文件加载
	path := s.getPath(digest)
	metaPath := path + ".meta"

	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		return Descriptor{}, ErrNotFound
	}

	var fileMeta blobMeta
	if err := json.Unmarshal(metaBytes, &fileMeta); err != nil {
		os.Remove(path)
		os.Remove(metaPath)
		return Descriptor{}, ErrNotFound
	}

	if time.Now().After(fileMeta.ExpiresAt) {
		os.Remove(path)
		os.Remove(metaPath)
		return Descriptor{}, ErrExpired
	}

	// 更新索引
	s.mu.Lock()
	s.index[digest] = &fileMeta
	s.mu.Unlock()

	return Descriptor{
		Digest:    fileMeta.Digest,
		Size:      fileMeta.Size,
		MediaType: fileMeta.MediaType,
	}, nil
}

// Get 获取 blob 内容
func (s *FileBlobStore) Get(ctx context.Context, digest string) (io.ReadCloser, error) {
	// 先检查是否存在
	if _, err := s.Stat(ctx, digest); err != nil {
		return nil, err
	}

	path := s.getPath(digest)
	file, err := os.Open(path)
	if err != nil {
		return nil, ErrNotFound
	}

	return file, nil
}

// Put 存储 blob
func (s *FileBlobStore) Put(ctx context.Context, digest string, content io.Reader, size int64) error {
	path := s.getPath(digest)
	dir := filepath.Dir(path)

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	// 使用临时文件写入
	tmpFile, err := os.CreateTemp(dir, "blob-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// 使用缓冲写入
	writer := bufio.NewWriterSize(tmpFile, 256*1024)

	// 同时计算哈希验证
	hasher := sha256.New()
	tee := io.TeeReader(content, hasher)

	written, err := io.Copy(writer, tee)
	if err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to write content: %w", err)
	}

	if err := writer.Flush(); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return fmt.Errorf("failed to flush: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to close: %w", err)
	}

	// 验证哈希
	actualHash := "sha256:" + hex.EncodeToString(hasher.Sum(nil))
	if digest != "" && digest != actualHash {
		os.Remove(tmpPath)
		return fmt.Errorf("digest mismatch: expected %s, got %s", digest, actualHash)
	}

	// 移动到最终位置
	if err := os.Rename(tmpPath, path); err != nil {
		// 可能跨文件系统，尝试复制
		if err := copyFile(tmpPath, path); err != nil {
			os.Remove(tmpPath)
			return fmt.Errorf("failed to move file: %w", err)
		}
		os.Remove(tmpPath)
	}

	// 保存元数据
	now := time.Now()
	meta := &blobMeta{
		Digest:    digest,
		Size:      written,
		CachedAt:  now,
		ExpiresAt: now.Add(s.ttl),
		FilePath:  path,
	}

	metaBytes, _ := json.Marshal(meta)
	if err := os.WriteFile(path+".meta", metaBytes, 0o644); err != nil {
		// 元数据保存失败不是致命错误
		fmt.Printf("Warning: failed to save blob metadata: %v\n", err)
	}

	// 更新索引
	s.mu.Lock()
	s.index[digest] = meta
	s.mu.Unlock()

	return nil
}

// Delete 删除 blob
func (s *FileBlobStore) Delete(ctx context.Context, digest string) error {
	s.mu.Lock()
	delete(s.index, digest)
	s.mu.Unlock()

	path := s.getPath(digest)
	os.Remove(path)
	os.Remove(path + ".meta")

	return nil
}

// Cleanup 清理过期和超大小的缓存
func (s *FileBlobStore) Cleanup(maxSize int64) int {
	now := time.Now()
	var toDelete []string
	var totalSize int64

	s.mu.RLock()
	for digest, meta := range s.index {
		if now.After(meta.ExpiresAt) {
			toDelete = append(toDelete, digest)
		} else {
			totalSize += meta.Size
		}
	}
	s.mu.RUnlock()

	// 删除过期项
	for _, digest := range toDelete {
		s.Delete(context.Background(), digest)
	}

	// 如果超过大小限制，按 LRU 删除（简化实现：随机删除）
	if totalSize > maxSize {
		s.mu.RLock()
		for digest := range s.index {
			if totalSize <= maxSize {
				break
			}
			if meta, ok := s.index[digest]; ok {
				totalSize -= meta.Size
				toDelete = append(toDelete, digest)
			}
		}
		s.mu.RUnlock()

		for _, digest := range toDelete {
			s.Delete(context.Background(), digest)
		}
	}

	return len(toDelete)
}

// LoadIndex 加载现有缓存索引
func (s *FileBlobStore) LoadIndex() (count int64, manifestCount int64, totalSize int64) {
	filepath.Walk(s.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		// 只处理 .meta 文件
		if !strings.HasSuffix(path, ".meta") {
			return nil
		}

		metaBytes, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		var meta blobMeta
		if err := json.Unmarshal(metaBytes, &meta); err != nil {
			return nil
		}

		// 检查是否过期
		if time.Now().After(meta.ExpiresAt) {
			dataPath := strings.TrimSuffix(path, ".meta")
			os.Remove(path)
			os.Remove(dataPath)
			return nil
		}

		// 加入索引
		s.mu.Lock()
		s.index[meta.Digest] = &meta
		s.mu.Unlock()

		count++
		totalSize += meta.Size

		return nil
	})

	return count, 0, totalSize
}

// getPath 获取 blob 文件路径
func (s *FileBlobStore) getPath(digest string) string {
	// 移除 sha256: 前缀
	hash := strings.TrimPrefix(digest, "sha256:")
	if len(hash) < 4 {
		hash = hashKey(digest)
	}
	// 使用前 4 个字符分层
	return filepath.Join(s.dir, hash[:2], hash[2:4], hash)
}

// =============================================================================
// FileManifestStore - 文件系统 Manifest 存储
// =============================================================================

// FileManifestStore 基于文件系统的 manifest 存储
type FileManifestStore struct {
	dir       string
	tagTTL    time.Duration
	digestTTL time.Duration

	mu    sync.RWMutex
	index map[string]*CacheEntry // repo/reference -> entry
}

// NewFileManifestStore 创建 manifest 存储
func NewFileManifestStore(dir string, tagTTL, digestTTL time.Duration) *FileManifestStore {
	return &FileManifestStore{
		dir:       dir,
		tagTTL:    tagTTL,
		digestTTL: digestTTL,
		index:     make(map[string]*CacheEntry),
	}
}

// Get 获取 manifest
func (s *FileManifestStore) Get(ctx context.Context, repo, reference string) (*CacheEntry, error) {
	key := s.getKey(repo, reference)

	// 先查内存索引
	s.mu.RLock()
	entry, ok := s.index[key]
	s.mu.RUnlock()

	if ok {
		if time.Now().Before(entry.ExpiresAt) {
			return entry, nil
		}
		// 已过期
		s.mu.Lock()
		delete(s.index, key)
		s.mu.Unlock()
	}

	// 从文件加载
	path := s.getPath(repo, reference)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, ErrNotFound
	}

	entry = &CacheEntry{}
	if err := json.Unmarshal(data, entry); err != nil {
		os.Remove(path)
		return nil, ErrNotFound
	}

	if time.Now().After(entry.ExpiresAt) {
		os.Remove(path)
		return nil, ErrExpired
	}

	// 更新索引
	s.mu.Lock()
	s.index[key] = entry
	s.mu.Unlock()

	return entry, nil
}

// Put 存储 manifest
func (s *FileManifestStore) Put(ctx context.Context, repo, reference string, entry *CacheEntry) error {
	key := s.getKey(repo, reference)
	path := s.getPath(repo, reference)

	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create directory: %w", err)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("failed to marshal entry: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("failed to write file: %w", err)
	}

	// 更新索引
	s.mu.Lock()
	s.index[key] = entry
	s.mu.Unlock()

	return nil
}

// Delete 删除 manifest
func (s *FileManifestStore) Delete(ctx context.Context, repo, reference string) error {
	key := s.getKey(repo, reference)

	s.mu.Lock()
	delete(s.index, key)
	s.mu.Unlock()

	path := s.getPath(repo, reference)
	return os.Remove(path)
}

// Cleanup 清理过期缓存
func (s *FileManifestStore) Cleanup() int {
	now := time.Now()
	var toDelete []string

	s.mu.RLock()
	for key, entry := range s.index {
		if now.After(entry.ExpiresAt) {
			toDelete = append(toDelete, key)
		}
	}
	s.mu.RUnlock()

	for _, key := range toDelete {
		s.mu.Lock()
		delete(s.index, key)
		s.mu.Unlock()
	}

	return len(toDelete)
}

// LoadIndex 加载现有缓存索引
func (s *FileManifestStore) LoadIndex() (count int64, totalSize int64) {
	filepath.Walk(s.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		var entry CacheEntry
		if err := json.Unmarshal(data, &entry); err != nil {
			os.Remove(path)
			return nil
		}

		if time.Now().After(entry.ExpiresAt) {
			os.Remove(path)
			return nil
		}

		// 从路径提取 key
		relPath, _ := filepath.Rel(s.dir, path)
		key := strings.ReplaceAll(relPath, string(filepath.Separator), "/")

		s.mu.Lock()
		s.index[key] = &entry
		s.mu.Unlock()

		count++
		totalSize += entry.Descriptor.Size

		return nil
	})

	return count, totalSize
}

func (s *FileManifestStore) getKey(repo, reference string) string {
	return repo + "/" + reference
}

func (s *FileManifestStore) getPath(repo, reference string) string {
	// 使用哈希避免文件名问题
	key := s.getKey(repo, reference)
	hash := hashKey(key)
	return filepath.Join(s.dir, hash[:2], hash[2:4], hash+".json")
}

// =============================================================================
// 辅助函数
// =============================================================================

func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	buf := make([]byte, 256*1024)
	_, err = io.CopyBuffer(dstFile, srcFile, buf)
	return err
}
