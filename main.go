package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

const (
	// 最大可缓存的响应大小 (50MB)，超过此大小的响应将直接流式传输不缓存
	maxCacheableSize = 50 * 1024 * 1024
	// 流式传输缓冲区大小 (256KB)，适合大文件传输
	streamBufferSize = 256 * 1024
)

type Config struct {
	Port                string
	CacheDir            string
	CacheEnabled        bool          // 缓存开关
	CacheManifestTTL    time.Duration // manifest by tag 缓存时间
	CacheBlobTTL        time.Duration // blob 缓存时间 (不可变内容)
	FollowAllRedirects  bool          // 跟随所有重定向（启用后可缓存外部存储内容）
	Debug               bool
	CustomDomain        string
	Routes              map[string]string
	BlockedHostPatterns []string // 黑名单域名模式
	DNSEnabled          bool     // 是否启用自定义DNS
	DNSServers          []string // DNS服务器列表
	DNSTimeout          string   // DNS查询超时时间
}

type ProxyServer struct {
	config       *Config
	cacheManager *CacheManager // 新的统一缓存管理器
	transport    *http.Transport
	server       *http.Server
}

func main() {
	// 添加健康检查命令行参数
	healthCheck := flag.Bool("health-check", false, "Perform health check")
	flag.Parse()

	if *healthCheck {
		performHealthCheck()
		return
	}

	server := NewProxyServer()

	// 优雅关闭
	go func() {
		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt, syscall.SIGTERM)
		<-c

		log.Println("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		if err := server.Shutdown(ctx); err != nil {
			log.Printf("Server shutdown error: %v", err)
		}
	}()

	server.Start()
}

func NewProxyServer() *ProxyServer {
	customDomain := getEnv("CUSTOM_DOMAIN", "example.com")

	// 内置黑名单：这些域名被墙，需要服务器端处理重定向
	// 注意：只包含被墙的域名，不包含可以正常访问的外部存储
	defaultBlockedHostPatterns := []string{
		"cloudflare.docker.com",
		"docker.com",
		"docker.io",
	}

	// 从环境变量加载额外的黑名单
	blockedHostPatterns := make([]string, len(defaultBlockedHostPatterns))
	copy(blockedHostPatterns, defaultBlockedHostPatterns)
	if externalBlocked := getEnv("BLOCKED_HOSTS", ""); externalBlocked != "" {
		externalPatterns := strings.Split(externalBlocked, ",")
		for _, pattern := range externalPatterns {
			pattern = strings.TrimSpace(pattern)
			if pattern != "" {
				blockedHostPatterns = append(blockedHostPatterns, pattern)
			}
		}
	}

	// 解析DNS服务器列表
	var dnsServers []string
	if dnsServersStr := getEnv("DNS_SERVERS", ""); dnsServersStr != "" {
		for _, server := range strings.Split(dnsServersStr, ",") {
			server = strings.TrimSpace(server)
			if server != "" {
				dnsServers = append(dnsServers, server)
			}
		}
	}

	// 解析缓存 TTL 配置
	manifestTTL := parseDuration(getEnv("CACHE_MANIFEST_TTL", "1d"), 24*time.Hour)
	blobTTL := parseDuration(getEnv("CACHE_BLOB_TTL", "1y"), 365*24*time.Hour) // 默认 1 年

	config := &Config{
		Port:                getEnv("PORT", "8080"),
		CacheDir:            getEnv("CACHE_DIR", "./cache"),
		CacheEnabled:        getEnv("CACHE_ENABLED", "true") == "true", // 默认启用缓存
		CacheManifestTTL:    manifestTTL,
		CacheBlobTTL:        blobTTL,
		FollowAllRedirects:  getEnv("FOLLOW_ALL_REDIRECTS", "false") == "true", // 跟随所有重定向以缓存
		Debug:               getEnv("DEBUG", "false") == "true",
		CustomDomain:        customDomain,
		Routes:              buildRoutes(customDomain),
		BlockedHostPatterns: blockedHostPatterns,
		DNSEnabled:          getEnv("DNS_ENABLED", "false") == "true",
		DNSServers:          dnsServers,
		DNSTimeout:          getEnv("DNS_TIMEOUT", "5s"),
	}

	// 初始化自定义DNS解析器
	initCustomDNS(config)

	// 配置高性能的 Transport（优化大文件传输）
	transport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		MaxConnsPerHost:       50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		DisableKeepAlives:     false,

		// TLS 配置
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: false,
			MinVersion:         tls.VersionTLS12,
		},

		// 启用 HTTP/2
		ForceAttemptHTTP2: true,

		// 禁用压缩，让客户端直接处理
		DisableCompression: true,

		// 增大写缓冲区，优化大文件传输
		WriteBufferSize: 256 * 1024, // 256KB
		ReadBufferSize:  256 * 1024, // 256KB
	}

	// 创建缓存管理器
	cacheConfig := &CacheConfig{
		Dir:             config.CacheDir,
		MaxSize:         10 * 1024 * 1024 * 1024, // 10GB
		ManifestTTL:     config.CacheManifestTTL,
		BlobTTL:         config.CacheBlobTTL,
		CleanupInterval: 30 * time.Minute,
		Debug:           config.Debug,
	}

	cacheManager, err := NewCacheManager(cacheConfig)
	if err != nil {
		log.Fatalf("Failed to create cache manager: %v", err)
	}

	return &ProxyServer{
		config:       config,
		cacheManager: cacheManager,
		transport:    transport,
	}
}

// 根据自定义域名构建路由映射，参考 ciiiii/cloudflare-docker-proxy
func buildRoutes(customDomain string) map[string]string {
	dockerHub := "https://registry-1.docker.io"

	routes := map[string]string{
		// production - 使用 ciiiii 版本的简洁命名规则
		fmt.Sprintf("docker.%s", customDomain):     dockerHub,
		fmt.Sprintf("quay.%s", customDomain):       "https://quay.io",
		fmt.Sprintf("gcr.%s", customDomain):        "https://gcr.io",
		fmt.Sprintf("k8s-gcr.%s", customDomain):    "https://k8s.gcr.io",
		fmt.Sprintf("k8s.%s", customDomain):        "https://registry.k8s.io",
		fmt.Sprintf("ghcr.%s", customDomain):       "https://ghcr.io",
		fmt.Sprintf("cloudsmith.%s", customDomain): "https://docker.cloudsmith.io",
		fmt.Sprintf("ecr.%s", customDomain):        "https://public.ecr.aws",

		// staging
		fmt.Sprintf("docker-staging.%s", customDomain): dockerHub,
	}

	return routes
}

func (p *ProxyServer) Start() {
	r := chi.NewRouter()

	// 添加中间件
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(60 * time.Second))

	if p.config.Debug {
		r.Use(middleware.RequestID)
		log.Println("[DEBUG] Debug mode enabled")
	}

	// 健康检查端点
	r.Get("/health", p.handleHealth)
	r.Get("/healthz", p.handleHealth)

	// 缓存统计端点
	r.Get("/stats", p.handleStats)
	r.Get("/stats/cache", p.handleCacheStats)

	// 路由定义
	r.Get("/", p.handleRoot)
	r.Route("/v2", func(r chi.Router) {
		r.Get("/", p.handleV2Root)
		r.Get("/auth", p.handleAuth)
		r.HandleFunc("/*", p.handleV2Request)
	})

	log.Printf("Starting proxy server on port %s", p.config.Port)
	log.Printf("Custom domain: %s", p.config.CustomDomain)
	log.Printf("Cache directory: %s", p.config.CacheDir)
	log.Printf("Cache enabled: %v", p.config.CacheEnabled)
	log.Printf("Debug mode: %v", p.config.Debug)

	// 打印路由配置
	if p.config.Debug {
		log.Println("Available routes:")
		for host, upstream := range p.config.Routes {
			log.Printf("  %s -> %s", host, upstream)
		}
	}

	p.server = &http.Server{
		Addr:    ":" + p.config.Port,
		Handler: r,

		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0, // 禁用写超时，支持大文件长时间传输
		IdleTimeout:       120 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    1 << 20, // 1MB
	}

	log.Fatal(p.server.ListenAndServe())
}

func (p *ProxyServer) Shutdown(ctx context.Context) error {
	if p.server != nil {
		return p.server.Shutdown(ctx)
	}
	return nil
}

// 健康检查处理器
func (p *ProxyServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	health := map[string]interface{}{
		"status":    "healthy",
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"version":   "1.0.0",
		"uptime":    time.Since(startTime).String(),
	}

	json.NewEncoder(w).Encode(health)
}

// 统计信息处理器
func (p *ProxyServer) handleStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	stats := map[string]interface{}{
		"uptime":  time.Since(startTime).String(),
		"enabled": p.config.CacheEnabled,
	}

	if p.cacheManager != nil {
		stats["cache"] = p.cacheManager.Stats()
	}

	json.NewEncoder(w).Encode(stats)
}

// 详细缓存统计
func (p *ProxyServer) handleCacheStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	stats := map[string]interface{}{
		"config": map[string]interface{}{
			"directory":   p.config.CacheDir,
			"manifestTTL": p.config.CacheManifestTTL.String(),
			"blobTTL":     p.config.CacheBlobTTL.String(),
			"enabled":     p.config.CacheEnabled,
		},
	}

	if p.cacheManager != nil {
		stats["stats"] = p.cacheManager.Stats()
	}

	json.NewEncoder(w).Encode(stats)
}

var startTime = time.Now()

// 执行健康检查
func performHealthCheck() {
	port := getEnv("PORT", "8080")
	url := fmt.Sprintf("http://localhost:%s/health", port)

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		log.Printf("Health check failed: %v", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Health check failed: status code %d", resp.StatusCode)
		os.Exit(1)
	}

	log.Println("Health check passed")
}

func (p *ProxyServer) handleRoot(w http.ResponseWriter, r *http.Request) {
	upstream := p.routeByHost(r.Host)
	if upstream == "" {
		// 返回可用路由信息，与原版保持一致
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"routes":  p.config.Routes,
			"message": "Available registry routes",
		})
		return
	}
	http.Redirect(w, r, "/v2/", http.StatusMovedPermanently)
}

func (p *ProxyServer) handleV2Root(w http.ResponseWriter, r *http.Request) {
	upstream := p.routeByHost(r.Host)
	if upstream == "" {
		if p.config.Debug {
			log.Printf("[DEBUG] No upstream found for host: %s", r.Host)
		}
		p.writeRoutesResponse(w)
		return
	}

	if p.config.Debug {
		log.Printf("[DEBUG] /v2/ request - Host: %s, Upstream: %s", r.Host, upstream)
	}

	upstreamURL, _ := url.Parse(upstream + "/v2/")
	req := p.createProxyRequest(r, upstreamURL)

	// 检查是否需要认证，添加重试机制
	var resp *http.Response
	var err error
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		if i > 0 {
			if p.config.Debug {
				log.Printf("[DEBUG] /v2/ retry attempt %d/%d", i+1, maxRetries)
			}
			time.Sleep(time.Duration(i) * 100 * time.Millisecond) // 递增延迟
			// 重新创建请求（因为 Body 可能已被读取）
			req = p.createProxyRequest(r, upstreamURL)
		}

		resp, err = p.transport.RoundTrip(req)
		if err == nil {
			break // 成功，退出重试
		}

		if p.config.Debug {
			log.Printf("[DEBUG] /v2/ RoundTrip error (attempt %d): %v", i+1, err)
		}

		// 如果不是最后一次尝试，继续重试
		if i < maxRetries-1 {
			if resp != nil {
				resp.Body.Close()
			}
			continue
		}
	}

	if err != nil {
		if p.config.Debug {
			log.Printf("[DEBUG] /v2/ RoundTrip failed after %d attempts: %v", maxRetries, err)
		}
		p.writeErrorResponse(w, fmt.Sprintf("upstream connection failed after %d attempts: %v", maxRetries, err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if p.config.Debug {
		log.Printf("[DEBUG] /v2/ response status: %d", resp.StatusCode)
	}

	// 如果返回 401，返回认证挑战
	if resp.StatusCode == http.StatusUnauthorized {
		if p.config.Debug {
			log.Printf("[DEBUG] /v2/ returning 401 auth challenge")
		}
		p.responseUnauthorized(w, r)
		return
	}

	p.copyResponseRoundTrip(w, resp)
}

func (p *ProxyServer) handleAuth(w http.ResponseWriter, r *http.Request) {
	upstream := p.routeByHost(r.Host)
	if upstream == "" {
		if p.config.Debug {
			log.Printf("[DEBUG] /v2/auth - No upstream found for host: %s", r.Host)
		}
		p.writeRoutesResponse(w)
		return
	}

	scope := r.URL.Query().Get("scope")
	if p.config.Debug {
		log.Printf("[DEBUG] /v2/auth - Host: %s, Upstream: %s, Scope: %s", r.Host, upstream, scope)
	}

	upstreamURL, _ := url.Parse(upstream + "/v2/")
	req := p.createProxyRequest(r, upstreamURL)
	req.Method = "GET"

	// 使用 RoundTrip 直接调用
	resp, err := p.transport.RoundTrip(req)
	if err != nil {
		if p.config.Debug {
			log.Printf("[DEBUG] /v2/auth RoundTrip error: %v", err)
		}
		p.writeErrorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		if p.config.Debug {
			log.Printf("[DEBUG] /v2/auth unexpected status: %d", resp.StatusCode)
		}
		p.copyResponseRoundTrip(w, resp)
		return
	}

	authenticateStr := resp.Header.Get("WWW-Authenticate")
	if authenticateStr == "" {
		if p.config.Debug {
			log.Printf("[DEBUG] /v2/auth missing WWW-Authenticate header")
		}
		p.copyResponseRoundTrip(w, resp)
		return
	}

	if p.config.Debug {
		log.Printf("[DEBUG] /v2/auth WWW-Authenticate: %s", authenticateStr)
	}

	wwwAuth, err := p.parseAuthenticate(authenticateStr)
	if err != nil {
		if p.config.Debug {
			log.Printf("[DEBUG] /v2/auth parse error: %v", err)
		}
		p.writeErrorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// 处理Docker Hub library镜像的scope
	originalScope := scope
	if strings.Contains(upstream, "registry-1.docker.io") && scope != "" {
		scope = p.processDockerHubScope(scope)
		if p.config.Debug && scope != originalScope {
			log.Printf("[DEBUG] /v2/auth scope rewritten: %s -> %s", originalScope, scope)
		}
	}

	token, err := p.fetchTokenWithRoundTrip(wwwAuth, scope, r.Header.Get("Authorization"))
	if err != nil {
		if p.config.Debug {
			log.Printf("[DEBUG] /v2/auth token fetch error: %v", err)
		}
		p.writeErrorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer token.Body.Close()

	if p.config.Debug {
		log.Printf("[DEBUG] /v2/auth token fetched successfully, status: %d", token.StatusCode)
	}

	p.copyResponseRoundTrip(w, token)
}

func (p *ProxyServer) handleV2Request(w http.ResponseWriter, r *http.Request) {
	upstream := p.routeByHost(r.Host)
	if upstream == "" {
		if p.config.Debug {
			log.Printf("[DEBUG] /v2/* No upstream found for host: %s, path: %s", r.Host, r.URL.Path)
		}
		p.writeRoutesResponse(w)
		return
	}

	if p.config.Debug {
		log.Printf("[DEBUG] /v2/* Request - Method: %s, Host: %s, Path: %s, Upstream: %s",
			r.Method, r.Host, r.URL.Path, upstream)
	}

	isDockerHub := strings.Contains(upstream, "registry-1.docker.io")

	// 处理Docker Hub library镜像重定向
	if isDockerHub {
		if redirectURL := p.processDockerHubLibraryRedirect(r.URL.Path); redirectURL != "" {
			if p.config.Debug {
				log.Printf("[DEBUG] /v2/* Library redirect: %s -> %s", r.URL.Path, redirectURL)
			}
			http.Redirect(w, r, redirectURL, http.StatusMovedPermanently)
			return
		}
	}

	// 生成缓存键
	cacheKey := CacheKey(r.Host, r.URL.Path)
	isCacheableRequest := IsCacheable(r.URL.Path)
	isBlob := strings.Contains(r.URL.Path, "/blobs/")

	// 检查缓存（如果启用）
	if p.config.CacheEnabled && isCacheableRequest && p.cacheManager != nil {
		// 对于 blob 使用流式传输
		if isBlob {
			if entry, reader, found := p.cacheManager.GetBlobReader(cacheKey); found {
				if p.config.Debug {
					log.Printf("[DEBUG] /v2/* Cache HIT (streaming): %s", r.URL.Path)
				}
				p.serveCachedBlobStream(w, entry, reader)
				return
			}
		} else {
			// manifest 等小文件使用内存缓存
			if entry, found := p.cacheManager.Get(cacheKey); found {
				if p.config.Debug {
					log.Printf("[DEBUG] /v2/* Cache HIT: %s", r.URL.Path)
				}
				p.serveCachedEntry(w, entry)
				return
			}
		}
		if p.config.Debug {
			log.Printf("[DEBUG] /v2/* Cache MISS: %s", r.URL.Path)
		}
	}

	// 请求去重：防止多个客户端同时拉取相同内容时重复请求上游
	// 类似 distribution/distribution 的 inflight 机制
	if p.config.CacheEnabled && isCacheableRequest && r.Method == "GET" && p.cacheManager != nil {
		first, wait, done := p.cacheManager.TryInflight(cacheKey)

		if !first {
			// 不是第一个请求，等待第一个请求完成
			if p.config.Debug {
				log.Printf("[DEBUG] /v2/* Waiting for inflight request: %s", r.URL.Path)
			}

			result, err := wait(r.Context())
			if err != nil {
				// 请求被取消
				if p.config.Debug {
					log.Printf("[DEBUG] /v2/* Inflight wait cancelled: %v", err)
				}
				p.writeErrorResponse(w, "request cancelled", http.StatusRequestTimeout)
				return
			}

			// 第一个请求已完成，从缓存获取结果
			if result != nil && result.Cached {
				// 对于 blob 使用流式传输
				if isBlob {
					if entry, reader, found := p.cacheManager.GetBlobReader(cacheKey); found {
						if p.config.Debug {
							log.Printf("[DEBUG] /v2/* Inflight cache HIT (streaming): %s", r.URL.Path)
						}
						p.serveCachedBlobStream(w, entry, reader)
						return
					}
				} else if entry, found := p.cacheManager.Get(cacheKey); found {
					if p.config.Debug {
						log.Printf("[DEBUG] /v2/* Inflight cache HIT: %s", r.URL.Path)
					}
					p.serveCachedEntry(w, entry)
					return
				}
			}

			// 缓存获取失败，回退到直接请求（不进入 inflight 追踪，因为第一个请求已失败）
			if p.config.Debug {
				log.Printf("[DEBUG] /v2/* Inflight fallback to direct request: %s", r.URL.Path)
			}
			// 回退请求不缓存，避免重复尝试缓存失败的内容
			upstreamURL, _ := url.Parse(upstream + r.URL.Path)
			upstreamURL.RawQuery = r.URL.RawQuery
			p.proxyRequestWithRoundTripAndKey(w, r, upstreamURL, false, "")
			return
		}

		// 是第一个请求，需要执行实际工作
		// 请求完成后调用 done 通知等待者
		defer func() {
			// 检查是否已缓存
			_, cached := p.cacheManager.Get(cacheKey)
			done(&InflightResult{
				CacheKey: cacheKey,
				Cached:   cached,
			})
		}()
	}

	// 转发请求
	upstreamURL, _ := url.Parse(upstream + r.URL.Path)
	upstreamURL.RawQuery = r.URL.RawQuery

	p.proxyRequestWithRoundTripAndKey(w, r, upstreamURL, true, cacheKey)
}

// proxyRequestWithRoundTripAndKey 使用 RoundTrip 进行底层代理控制（带缓存键）
func (p *ProxyServer) proxyRequestWithRoundTripAndKey(w http.ResponseWriter, r *http.Request, targetURL *url.URL, enableCache bool, cacheKey string) {
	if p.config.Debug {
		log.Printf("[DEBUG] Proxy request to: %s", targetURL.String())
	}

	// 创建代理请求
	req := p.createProxyRequest(r, targetURL)

	// 使用 RoundTrip 直接执行请求
	resp, err := p.transport.RoundTrip(req)
	if err != nil {
		if p.config.Debug {
			log.Printf("[DEBUG] Proxy RoundTrip error: %v", err)
		}
		p.writeErrorResponse(w, fmt.Sprintf("transport error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if p.config.Debug {
		log.Printf("[DEBUG] Proxy response status: %d from %s", resp.StatusCode, targetURL.Host)
	}

	// 处理认证
	if resp.StatusCode == http.StatusUnauthorized {
		if p.config.Debug {
			log.Printf("[DEBUG] Proxy got 401, returning auth challenge")
		}
		p.responseUnauthorized(w, r)
		return
	}

	// 处理重定向 (301, 302, 303, 307, 308)
	// 对于 AWS S3 等外部存储的重定向,直接返回给客户端让其直接下载
	// 这样避免代理服务器处理 AWS 签名等复杂问题
	if resp.StatusCode == http.StatusMovedPermanently ||
		resp.StatusCode == http.StatusFound ||
		resp.StatusCode == http.StatusSeeOther ||
		resp.StatusCode == http.StatusTemporaryRedirect ||
		resp.StatusCode == http.StatusPermanentRedirect {

		location := resp.Header.Get("Location")
		if location != "" {
			if p.config.Debug {
				log.Printf("[DEBUG] Proxy got redirect %d to: %s", resp.StatusCode, location)
			}

			// 检查重定向目标
			redirectURL, err := url.Parse(location)
			if err == nil {
				// 决定是否跟随重定向
				// 1. FOLLOW_ALL_REDIRECTS=true: 跟随所有重定向（用于缓存所有内容）
				// 2. 黑名单域名: 服务器端处理（被墙域名客户端无法访问）
				shouldFollow := p.config.FollowAllRedirects || p.isBlockedHost(redirectURL.Host)

				if shouldFollow {
					if p.config.Debug {
						if p.config.FollowAllRedirects {
							log.Printf("[DEBUG] FOLLOW_ALL_REDIRECTS enabled, following redirect to: %s", redirectURL.Host)
						} else {
							log.Printf("[DEBUG] Blocked host detected (%s), following redirect server-side", redirectURL.Host)
						}
					}
					// 跟随重定向并缓存内容
					p.followRedirectWithCache(w, r, redirectURL, cacheKey, enableCache)
					return
				}

				// 非黑名单域名:直接返回重定向响应给客户端
				// 这些域名可以正常访问 (如 AWS S3, Cloudflare R2, GCS, Azure Blob 等)
				// 让客户端自己处理重定向,减少代理服务器负担和流量
				if p.config.Debug {
					log.Printf("[DEBUG] Non-blocked host (%s), returning redirect to client", redirectURL.Host)
				}
				p.copyResponseRoundTrip(w, resp)
				return
			}
		}
	}

	shouldCache := p.config.CacheEnabled && enableCache && IsCacheable(r.URL.Path) && p.cacheManager != nil

	if shouldCache {
		// 使用传入的 cacheKey，如果为空则生成新的
		if cacheKey == "" {
			cacheKey = CacheKey(r.Host, r.URL.Path)
		}
		p.copyResponseWithCacheRoundTrip(w, resp, cacheKey, true)
	} else {
		p.copyResponseWithCacheRoundTrip(w, resp, "", false)
	}
}

// 检查域名是否在黑名单中
func (p *ProxyServer) isBlockedHost(host string) bool {
	for _, pattern := range p.config.BlockedHostPatterns {
		if strings.Contains(host, pattern) {
			if p.config.Debug {
				log.Printf("[DEBUG] Host %s matched blocked pattern: %s", host, pattern)
			}
			return true
		}
	}
	return false
}

// followRedirectWithCache 跟随重定向并支持缓存
// 用于 FOLLOW_ALL_REDIRECTS=true 场景，将外部存储内容缓存到本地
func (p *ProxyServer) followRedirectWithCache(w http.ResponseWriter, originalReq *http.Request, targetURL *url.URL, cacheKey string, enableCache bool) {
	p.followRedirectWithCacheInternal(w, originalReq, targetURL, cacheKey, enableCache, nil, 0)
}

func (p *ProxyServer) followRedirectWithCacheInternal(w http.ResponseWriter, originalReq *http.Request, targetURL *url.URL, cacheKey string, enableCache bool, originalHeaders http.Header, redirectCount int) {
	const maxRedirects = 10

	if redirectCount >= maxRedirects {
		if p.config.Debug {
			log.Printf("[DEBUG] Max redirects (%d) exceeded", maxRedirects)
		}
		p.writeErrorResponse(w, "too many redirects", http.StatusBadGateway)
		return
	}

	if p.config.Debug {
		log.Printf("[DEBUG] Following redirect with cache (%d/%d): %s", redirectCount+1, maxRedirects, targetURL.String())
	}

	// 创建新的 GET 请求，不带原始请求的认证信息
	req, err := http.NewRequest("GET", targetURL.String(), nil)
	if err != nil {
		if p.config.Debug {
			log.Printf("[DEBUG] Failed to create redirect request: %v", err)
		}
		p.writeErrorResponse(w, fmt.Sprintf("invalid redirect URL: %v", err), http.StatusBadGateway)
		return
	}

	// 设置 User-Agent
	req.Header.Set("User-Agent", "go-docker-proxy/1.0")

	// 保留 Accept 和 Range headers
	if originalHeaders != nil {
		if accept := originalHeaders.Get("Accept"); accept != "" {
			req.Header.Set("Accept", accept)
		}
		if rangeHeader := originalHeaders.Get("Range"); rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
	} else if originalReq != nil {
		// 从原始请求获取
		if accept := originalReq.Header.Get("Accept"); accept != "" {
			req.Header.Set("Accept", accept)
		}
		if rangeHeader := originalReq.Header.Get("Range"); rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
	}

	// 使用 RoundTrip 执行请求
	resp, err := p.transport.RoundTrip(req)
	if err != nil {
		if p.config.Debug {
			log.Printf("[DEBUG] Redirect request error: %v", err)
		}
		p.writeErrorResponse(w, fmt.Sprintf("redirect request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if p.config.Debug {
		log.Printf("[DEBUG] Redirect response status: %d, Content-Length: %d", resp.StatusCode, resp.ContentLength)
	}

	// 处理嵌套重定向
	if resp.StatusCode == http.StatusMovedPermanently ||
		resp.StatusCode == http.StatusFound ||
		resp.StatusCode == http.StatusSeeOther ||
		resp.StatusCode == http.StatusTemporaryRedirect ||
		resp.StatusCode == http.StatusPermanentRedirect {

		location := resp.Header.Get("Location")
		if location != "" {
			nextURL, err := url.Parse(location)
			if err == nil {
				p.followRedirectWithCacheInternal(w, originalReq, nextURL, cacheKey, enableCache, req.Header, redirectCount+1)
				return
			}
		}
	}

	// 使用带缓存的响应处理
	shouldCache := p.config.CacheEnabled && enableCache && cacheKey != "" && p.cacheManager != nil
	if shouldCache {
		p.copyResponseWithCacheRoundTrip(w, resp, cacheKey, true)
	} else {
		p.copyResponseRoundTrip(w, resp)
	}
}

// 跟随签名 URL 重定向 (用于 AWS S3/Cloudflare R2 等外部存储)
// followRedirectWithSignedURL 跟随签名 URL 重定向 (用于被墙域名)
// 类似 distribution/distribution 的 checkHTTPRedirect，支持嵌套重定向并保留关键 headers
func (p *ProxyServer) followRedirectWithSignedURL(w http.ResponseWriter, signedURL *url.URL) {
	p.followRedirectWithSignedURLAndHeaders(w, signedURL, nil, 0)
}

// followRedirectWithSignedURLAndHeaders 跟随重定向，保留 Accept 和 Range headers
// maxRedirects: 最大重定向次数，类似 distribution 的 10 次限制
func (p *ProxyServer) followRedirectWithSignedURLAndHeaders(w http.ResponseWriter, targetURL *url.URL, originalHeaders http.Header, redirectCount int) {
	const maxRedirects = 10

	if redirectCount >= maxRedirects {
		if p.config.Debug {
			log.Printf("[DEBUG] Max redirects (%d) exceeded", maxRedirects)
		}
		p.writeErrorResponse(w, "too many redirects", http.StatusBadGateway)
		return
	}

	if p.config.Debug {
		log.Printf("[DEBUG] Following redirect (%d/%d): %s", redirectCount+1, maxRedirects, targetURL.String())
	}

	// 创建新的 GET 请求，不带原始请求的认证信息
	req, err := http.NewRequest("GET", targetURL.String(), nil)
	if err != nil {
		if p.config.Debug {
			log.Printf("[DEBUG] Failed to create redirect request: %v", err)
		}
		p.writeErrorResponse(w, fmt.Sprintf("invalid redirect URL: %v", err), http.StatusBadGateway)
		return
	}

	// 设置 User-Agent
	req.Header.Set("User-Agent", "go-docker-proxy/1.0")

	// 保留 Accept 和 Range headers（类似 distribution/distribution 的做法）
	if originalHeaders != nil {
		if accept := originalHeaders.Get("Accept"); accept != "" {
			req.Header.Set("Accept", accept)
		}
		if rangeHeader := originalHeaders.Get("Range"); rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
	}

	// 使用 RoundTrip 执行请求（不自动跟随重定向）
	resp, err := p.transport.RoundTrip(req)
	if err != nil {
		if p.config.Debug {
			log.Printf("[DEBUG] Redirect request error: %v", err)
		}
		p.writeErrorResponse(w, fmt.Sprintf("redirect request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if p.config.Debug {
		log.Printf("[DEBUG] Redirect response status: %d", resp.StatusCode)
	}

	// 处理嵌套重定向
	if resp.StatusCode == http.StatusMovedPermanently ||
		resp.StatusCode == http.StatusFound ||
		resp.StatusCode == http.StatusSeeOther ||
		resp.StatusCode == http.StatusTemporaryRedirect ||
		resp.StatusCode == http.StatusPermanentRedirect {

		location := resp.Header.Get("Location")
		if location != "" {
			nextURL, err := url.Parse(location)
			if err == nil {
				// 继续跟随重定向
				p.followRedirectWithSignedURLAndHeaders(w, nextURL, req.Header, redirectCount+1)
				return
			}
		}
	}

	// 如果返回 400/403，说明签名问题，记录详细错误
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden {
		if p.config.Debug {
			bodyBytes, _ := io.ReadAll(resp.Body)
			log.Printf("[DEBUG] Redirect error response: %s", string(bodyBytes))
			// 重新创建响应 body
			resp.Body = io.NopCloser(strings.NewReader(string(bodyBytes)))
		}
	}

	// 返回最终响应
	p.copyResponseRoundTrip(w, resp)
}

// 使用 RoundTrip 获取 token
func (p *ProxyServer) fetchTokenWithRoundTrip(wwwAuth map[string]string, scope, authorization string) (*http.Response, error) {
	tokenURL, err := url.Parse(wwwAuth["realm"])
	if err != nil {
		return nil, err
	}

	q := tokenURL.Query()
	if service, exists := wwwAuth["service"]; exists && service != "" {
		q.Set("service", service)
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	tokenURL.RawQuery = q.Encode()

	req, err := http.NewRequest("GET", tokenURL.String(), nil)
	if err != nil {
		return nil, err
	}

	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	// 设置 User-Agent
	req.Header.Set("User-Agent", "go-docker-proxy/1.0")

	return p.transport.RoundTrip(req)
}

func (p *ProxyServer) routeByHost(host string) string {
	originalHost := host
	// 移除端口号
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}

	if upstream, exists := p.config.Routes[host]; exists {
		if p.config.Debug {
			log.Printf("[DEBUG] Route matched: %s -> %s", originalHost, upstream)
		}
		return upstream
	}

	// 调试模式下的默认上游
	if p.config.Debug {
		log.Printf("[DEBUG] No route found for host: %s", originalHost)
		if targetUpstream := getEnv("TARGET_UPSTREAM", ""); targetUpstream != "" {
			log.Printf("[DEBUG] 使用 TARGET_UPSTREAM: %s", targetUpstream)
			return targetUpstream
		}
	}

	return ""
}

func (p *ProxyServer) processDockerHubLibraryRedirect(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) == 5 && parts[1] == "v2" {
		newPath := strings.Join(append(parts[:2], append([]string{"library"}, parts[2:]...)...), "/")
		if p.config.Debug {
			log.Printf("[DEBUG] Docker Hub library redirect: %s -> %s", path, newPath)
		}
		return newPath
	}
	return ""
}

func (p *ProxyServer) processDockerHubScope(scope string) string {
	parts := strings.Split(scope, ":")
	if len(parts) == 3 && !strings.Contains(parts[1], "/") {
		newScope := strings.Join([]string{parts[0], "library/" + parts[1], parts[2]}, ":")
		if p.config.Debug {
			log.Printf("[DEBUG] Docker Hub scope rewrite: %s -> %s", scope, newScope)
		}
		return newScope
	}
	return scope
}

func (p *ProxyServer) parseAuthenticate(authenticateStr string) (map[string]string, error) {
	re := regexp.MustCompile(`(\w+)="([^"]*)"`)
	matches := re.FindAllStringSubmatch(authenticateStr, -1)

	result := make(map[string]string)
	for _, match := range matches {
		if len(match) == 3 {
			result[match[1]] = match[2]
		}
	}

	if _, hasRealm := result["realm"]; !hasRealm {
		return nil, fmt.Errorf("invalid WWW-Authenticate header: %s", authenticateStr)
	}

	return result, nil
}

func (p *ProxyServer) responseUnauthorized(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	// 使用 hostname 而不是 host（与原版保持一致）
	hostname := r.Host
	if idx := strings.Index(hostname, ":"); idx != -1 {
		hostname = hostname[:idx]
	}

	var authHeader string
	if p.config.Debug {
		authHeader = fmt.Sprintf(`Bearer realm="%s://%s/v2/auth",service="go-docker-proxy"`, scheme, r.Host)
	} else {
		authHeader = fmt.Sprintf(`Bearer realm="%s://%s/v2/auth",service="go-docker-proxy"`, scheme, hostname)
	}

	w.Header().Set("WWW-Authenticate", authHeader)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)

	json.NewEncoder(w).Encode(map[string]string{
		"message": "UNAUTHORIZED",
	})
}

func (p *ProxyServer) createProxyRequest(originalReq *http.Request, targetURL *url.URL) *http.Request {
	var body io.Reader
	if originalReq.Body != nil {
		body = originalReq.Body
	}

	req, _ := http.NewRequestWithContext(
		originalReq.Context(),
		originalReq.Method,
		targetURL.String(),
		body,
	)

	// 复制关键请求头，过滤不需要的头
	skipHeaders := map[string]bool{
		"Connection":       true,
		"Proxy-Connection": true,
		"Upgrade":          true,
		"Host":             true,
		"Content-Length":   true, // 让 Transport 自动处理
	}

	for key, values := range originalReq.Header {
		if !skipHeaders[key] {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
	}

	// 设置正确的 Host
	req.Host = targetURL.Host
	req.Header.Set("Host", targetURL.Host)

	// 设置 User-Agent
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "go-docker-proxy/1.0")
	}

	return req
}

// 专门为 RoundTrip 优化的响应复制（支持大文件流式传输）
func (p *ProxyServer) copyResponseRoundTrip(w http.ResponseWriter, resp *http.Response) {
	// 复制响应头，过滤不需要的头
	skipHeaders := map[string]bool{
		"Connection":        true,
		"Proxy-Connection":  true,
		"Upgrade":           true,
		"Transfer-Encoding": true,
		"Content-Length":    false, // 保留 Content-Length
	}

	for key, values := range resp.Header {
		if !skipHeaders[key] {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
	}

	w.WriteHeader(resp.StatusCode)

	// 使用大缓冲区流式传输，支持大文件
	if resp.Body != nil {
		p.streamCopy(w, resp.Body)
	}
}

// streamCopy 高效流式复制，支持大文件传输
func (p *ProxyServer) streamCopy(dst io.Writer, src io.Reader) (written int64, err error) {
	// 使用 bufio 包装，提高读取效率
	bufReader := bufio.NewReaderSize(src, streamBufferSize)
	buf := make([]byte, streamBufferSize)

	// 尝试获取 Flusher 接口，用于实时刷新数据到客户端
	flusher, canFlush := dst.(http.Flusher)

	for {
		nr, readErr := bufReader.Read(buf)
		if nr > 0 {
			nw, writeErr := dst.Write(buf[:nr])
			if nw > 0 {
				written += int64(nw)
			}
			if writeErr != nil {
				err = writeErr
				break
			}
			if nr != nw {
				err = io.ErrShortWrite
				break
			}
			// 定期刷新，确保数据及时发送到客户端
			if canFlush {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				err = readErr
			}
			break
		}
	}
	return written, err
}

// 带缓存的响应复制（RoundTrip版本，支持大文件流式传输）
func (p *ProxyServer) copyResponseWithCacheRoundTrip(w http.ResponseWriter, resp *http.Response, cacheKey string, shouldStore bool) {
	skipHeaders := map[string]bool{
		"Connection":        true,
		"Proxy-Connection":  true,
		"Upgrade":           true,
		"Transfer-Encoding": true,
	}

	headersToCache := make(map[string][]string)
	for key, values := range resp.Header {
		if skipHeaders[key] {
			continue
		}
		headersToCache[key] = append(headersToCache[key], values...)
	}

	for key, values := range headersToCache {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	if resp.Body == nil {
		w.WriteHeader(resp.StatusCode)
		return
	}

	// HEAD 请求不缓存（没有 body），直接返回
	if resp.Request != nil && resp.Request.Method == "HEAD" {
		w.WriteHeader(resp.StatusCode)
		return
	}

	// 不需要缓存或非 200 响应，直接流式传输
	if !shouldStore || resp.StatusCode != http.StatusOK || p.cacheManager == nil {
		w.WriteHeader(resp.StatusCode)
		if _, err := p.streamCopy(w, resp.Body); err != nil {
			if p.config.Debug {
				log.Printf("[DEBUG] Stream copy error: %v", err)
			}
		}
		return
	}

	// 检查 Content-Length，判断是否为大文件
	contentLength := resp.ContentLength
	if contentLength < 0 {
		// 尝试从 Header 获取
		if clStr := resp.Header.Get("Content-Length"); clStr != "" {
			if cl, err := strconv.ParseInt(clStr, 10, 64); err == nil {
				contentLength = cl
			}
		}
	}

	// 大文件：直接流式传输，不缓存到内存
	if contentLength > maxCacheableSize || contentLength < 0 {
		if p.config.Debug {
			if contentLength > 0 {
				log.Printf("[DEBUG] Large file detected (%d bytes), streaming without memory cache: %s",
					contentLength, cacheKey)
			} else {
				log.Printf("[DEBUG] Unknown content length, streaming without memory cache: %s", cacheKey)
			}
		}
		w.Header().Set("X-Cache", "BYPASS")
		w.WriteHeader(resp.StatusCode)
		if _, err := p.streamCopy(w, resp.Body); err != nil {
			if p.config.Debug {
				log.Printf("[DEBUG] Large file stream error: %v", err)
			}
		}
		return
	}

	// 小文件：读取到内存并缓存
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		w.WriteHeader(resp.StatusCode)
		if len(bodyBytes) > 0 {
			_, _ = w.Write(bodyBytes)
		}
		if p.config.Debug {
			log.Printf("[DEBUG] Cache read error: %v", err)
		}
		return
	}

	// 验证响应内容：只缓存有效的响应
	if len(bodyBytes) == 0 {
		if p.config.Debug {
			log.Printf("[DEBUG] Skipping cache for empty response: %s", cacheKey)
		}
		w.WriteHeader(resp.StatusCode)
		return
	}

	headersToCache["Content-Length"] = []string{strconv.Itoa(len(bodyBytes))}

	w.Header().Set("X-Cache", "MISS")
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(bodyBytes)

	// 异步存储到缓存
	go func() {
		// 获取 mediaType
		mediaType := ""
		if ct, ok := headersToCache["Content-Type"]; ok && len(ct) > 0 {
			mediaType = ct[0]
		}

		entry := &CacheEntry{
			Descriptor: Descriptor{
				Size:      int64(len(bodyBytes)),
				MediaType: mediaType,
			},
			Data:       bodyBytes,
			Headers:    headersToCache,
			StatusCode: resp.StatusCode,
			CachedAt:   time.Now(),
			ExpiresAt:  time.Now().Add(1 * time.Hour),
		}
		p.cacheManager.Put(cacheKey, entry)
	}()
}

// serveCachedEntry 提供缓存响应（用于小文件如 manifest）
func (p *ProxyServer) serveCachedEntry(w http.ResponseWriter, entry *CacheEntry) {
	for key, values := range entry.Headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.Header().Set("X-Cache", "HIT")
	w.WriteHeader(entry.StatusCode)
	if len(entry.Data) > 0 {
		_, _ = w.Write(entry.Data)
	}
}

// serveCachedBlobStream 流式提供 blob 缓存响应（用于大文件）
func (p *ProxyServer) serveCachedBlobStream(w http.ResponseWriter, entry *CacheEntry, reader io.ReadCloser) {
	defer reader.Close()

	for key, values := range entry.Headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.Header().Set("X-Cache", "HIT")
	w.WriteHeader(entry.StatusCode)

	// 使用流式复制，不占用大量内存
	if _, err := p.streamCopy(w, reader); err != nil {
		if p.config.Debug {
			log.Printf("[DEBUG] Blob stream copy error: %v", err)
		}
	}
}

func (p *ProxyServer) writeRoutesResponse(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"routes":  p.config.Routes,
		"message": "Available registry routes",
	})
}

func (p *ProxyServer) writeErrorResponse(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(map[string]string{
		"error": message,
	})
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// parseDuration 解析时间间隔字符串，支持扩展格式
// 支持格式: 1h, 24h, 1d, 7d, 30d, 1y, 365d 等
// 标准格式: h(小时), m(分钟), s(秒)
// 扩展格式: d(天), w(周), M(月=30天), y(年=365天)
func parseDuration(s string, defaultValue time.Duration) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return defaultValue
	}

	// 先尝试标准格式
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}

	// 处理扩展格式
	var multiplier time.Duration
	var numStr string

	switch {
	case strings.HasSuffix(s, "y"):
		multiplier = 365 * 24 * time.Hour
		numStr = strings.TrimSuffix(s, "y")
	case strings.HasSuffix(s, "M"):
		multiplier = 30 * 24 * time.Hour
		numStr = strings.TrimSuffix(s, "M")
	case strings.HasSuffix(s, "w"):
		multiplier = 7 * 24 * time.Hour
		numStr = strings.TrimSuffix(s, "w")
	case strings.HasSuffix(s, "d"):
		multiplier = 24 * time.Hour
		numStr = strings.TrimSuffix(s, "d")
	default:
		return defaultValue
	}

	num, err := strconv.ParseFloat(strings.TrimSpace(numStr), 64)
	if err != nil || num < 0 {
		return defaultValue
	}

	return time.Duration(float64(multiplier) * num)
}
