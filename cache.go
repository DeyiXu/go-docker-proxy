package main

import (
	"crypto/md5"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type CacheItem struct {
	Data       []byte
	Headers    map[string][]string
	StatusCode int
	ExpiresAt  time.Time
}

type cacheMetadata struct {
	ExpiresAt  int64               `json:"expiresAt"`
	StatusCode int                 `json:"statusCode"`
	Headers    map[string][]string `json:"headers"`
}

type FileCache struct {
	cacheDir string
	items    map[string]*CacheItem
	mutex    sync.RWMutex
}

func NewFileCache(cacheDir string) *FileCache {
	_ = os.MkdirAll(cacheDir, 0o755)

	cache := &FileCache{
		cacheDir: cacheDir,
		items:    make(map[string]*CacheItem),
	}

	go cache.cleanupLoop()

	return cache
}

func (c *FileCache) Set(key string, data []byte, headers map[string][]string, statusCode int, ttl time.Duration) {
	expiresAt := time.Now().Add(ttl)

	dataCopy := make([]byte, len(data))
	copy(dataCopy, data)

	headersCopy := make(map[string][]string, len(headers))
	for k, values := range headers {
		copiedValues := make([]string, len(values))
		copy(copiedValues, values)
		headersCopy[k] = copiedValues
	}

	c.mutex.Lock()
	c.items[key] = &CacheItem{
		Data:       dataCopy,
		Headers:    headersCopy,
		StatusCode: statusCode,
		ExpiresAt:  expiresAt,
	}
	c.mutex.Unlock()

	go c.saveToFile(key, dataCopy, headersCopy, statusCode, expiresAt)
}

func (c *FileCache) Get(key string) (*CacheItem, bool) {
	c.mutex.RLock()
	item, exists := c.items[key]
	c.mutex.RUnlock()

	if exists {
		if time.Now().Before(item.ExpiresAt) {
			return item, true
		}

		c.mutex.Lock()
		delete(c.items, key)
		c.mutex.Unlock()
	}

	return c.loadFromFile(key)
}

func (c *FileCache) saveToFile(key string, data []byte, headers map[string][]string, statusCode int, expiresAt time.Time) {
	dataPath := c.getFilename(key)
	metaPath := dataPath + ".metadata"
	dir := filepath.Dir(dataPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Printf("Failed to create cache directory %s: %v\n", dir, err)
		return
	}

	if err := os.WriteFile(dataPath, data, 0o644); err != nil {
		fmt.Printf("Failed to save cache data %s: %v\n", dataPath, err)
		_ = os.Remove(dataPath)
		return
	}

	meta := cacheMetadata{
		ExpiresAt:  expiresAt.Unix(),
		StatusCode: statusCode,
		Headers:    headers,
	}

	metaBytes, err := json.Marshal(meta)
	if err != nil {
		fmt.Printf("Failed to marshal metadata for cache file %s: %v\n", dataPath, err)
		_ = os.Remove(dataPath)
		return
	}

	if err := os.WriteFile(metaPath, metaBytes, 0o644); err != nil {
		fmt.Printf("Failed to save cache metadata %s: %v\n", metaPath, err)
		_ = os.Remove(dataPath)
		_ = os.Remove(metaPath)
	}
}

func (c *FileCache) loadFromFile(key string) (*CacheItem, bool) {
	dataPath := c.getFilename(key)
	metaPath := dataPath + ".metadata"

	metaBytes, err := os.ReadFile(metaPath)
	if err != nil {
		_ = os.Remove(dataPath)
		_ = os.Remove(metaPath)
		return nil, false
	}

	var meta cacheMetadata
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		_ = os.Remove(dataPath)
		_ = os.Remove(metaPath)
		return nil, false
	}

	if time.Now().Unix() > meta.ExpiresAt {
		_ = os.Remove(dataPath)
		_ = os.Remove(metaPath)
		return nil, false
	}

	content, err := os.ReadFile(dataPath)
	if err != nil {
		_ = os.Remove(dataPath)
		_ = os.Remove(metaPath)
		return nil, false
	}

	dataCopy := make([]byte, len(content))
	copy(dataCopy, content)

	headersCopy := make(map[string][]string, len(meta.Headers))
	for k, values := range meta.Headers {
		copied := make([]string, len(values))
		copy(copied, values)
		headersCopy[k] = copied
	}

	item := &CacheItem{
		Data:       dataCopy,
		Headers:    headersCopy,
		StatusCode: meta.StatusCode,
		ExpiresAt:  time.Unix(meta.ExpiresAt, 0),
	}

	c.mutex.Lock()
	c.items[key] = item
	c.mutex.Unlock()

	return item, true
}

func (c *FileCache) getFilename(key string) string {
	hash := fmt.Sprintf("%x", md5.Sum([]byte(key)))
	return filepath.Join(c.cacheDir, hash[:2], hash[2:4], hash)
}

func (c *FileCache) cleanupLoop() {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		c.cleanup()
	}
}

func (c *FileCache) cleanup() {
	c.mutex.Lock()
	now := time.Now()
	for key, item := range c.items {
		if now.After(item.ExpiresAt) {
			delete(c.items, key)
			dataPath := c.getFilename(key)
			_ = os.Remove(dataPath)
			_ = os.Remove(dataPath + ".metadata")
		}
	}
	c.mutex.Unlock()
}
