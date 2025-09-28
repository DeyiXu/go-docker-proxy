package main

import (
	"crypto/md5"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type CacheItem struct {
	Data      []byte
	ExpiresAt time.Time
}

type FileCache struct {
	cacheDir string
	items    map[string]*CacheItem
	mutex    sync.RWMutex
}

func NewFileCache(cacheDir string) *FileCache {
	os.MkdirAll(cacheDir, 0755)
	
	cache := &FileCache{
		cacheDir: cacheDir,
		items:    make(map[string]*CacheItem),
	}
	
	// 启动清理协程
	go cache.cleanupLoop()
	
	return cache
}

func (c *FileCache) Set(key string, data []byte, ttl time.Duration) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	
	expiresAt := time.Now().Add(ttl)
	
	// 内存缓存
	c.items[key] = &CacheItem{
		Data:      data,
		ExpiresAt: expiresAt,
	}
	
	// 文件缓存
	go c.saveToFile(key, data, expiresAt)
}

func (c *FileCache) Get(key string) ([]byte, bool) {
	c.mutex.RLock()
	
	// 先检查内存缓存
	if item, exists := c.items[key]; exists {
		if time.Now().Before(item.ExpiresAt) {
			c.mutex.RUnlock()
			return item.Data, true
		}
		// 过期了，删除
		delete(c.items, key)
	}
	c.mutex.RUnlock()
	
	// 检查文件缓存
	return c.loadFromFile(key)
}

func (c *FileCache) saveToFile(key string, data []byte, expiresAt time.Time) {
	filename := c.getFilename(key)
	dir := filepath.Dir(filename)
	os.MkdirAll(dir, 0755)
	
	// 创建缓存文件，包含过期时间
	cacheData := fmt.Sprintf("%d\n", expiresAt.Unix())
	err := ioutil.WriteFile(filename, append([]byte(cacheData), data...), 0644)
	if err != nil {
		fmt.Printf("Failed to save cache file %s: %v\n", filename, err)
	}
}

func (c *FileCache) loadFromFile(key string) ([]byte, bool) {
	filename := c.getFilename(key)
	
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, false
	}
	
	// 解析过期时间
	lines := string(data)
	newlineIdx := -1
	for i, char := range lines {
		if char == '\n' {
			newlineIdx = i
			break
		}
	}
	
	if newlineIdx == -1 {
		return nil, false
	}
	
	var expiresAt int64
	_, err = fmt.Sscanf(lines[:newlineIdx], "%d", &expiresAt)
	if err != nil {
		return nil, false
	}
	
	// 检查是否过期
	if time.Now().Unix() > expiresAt {
		os.Remove(filename)
		return nil, false
	}
	
	content := data[newlineIdx+1:]
	
	// 添加到内存缓存
	c.mutex.Lock()
	c.items[key] = &CacheItem{
		Data:      content,
		ExpiresAt: time.Unix(expiresAt, 0),
	}
	c.mutex.Unlock()
	
	return content, true
}

func (c *FileCache) getFilename(key string) string {
	hash := fmt.Sprintf("%x", md5.Sum([]byte(key)))
	// 创建两级目录结构，避免单个目录文件过多
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
			// 删除对应的文件
			filename := c.getFilename(key)
			os.Remove(filename)
		}
	}
	c.mutex.Unlock()
}