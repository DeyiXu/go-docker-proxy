package main

import (
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

type Config struct {
	Port                string
	CacheDir            string
	CacheEnabled        bool // 缓存开关
	Debug               bool
	CustomDomain        string
	Routes              map[string]string
	BlockedHostPatterns []string // 黑名单域名模式
	DNSEnabled          bool     // 是否启用自定义DNS
	DNSServers          []string // DNS服务器列表
	DNSTimeout          string   // DNS查询超时时间
}

type ProxyServer struct {
	config    *Config
	cache     *FileCache
	transport *http.Transport
	server    *http.Server
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

	config := &Config{
		Port:                getEnv("PORT", "8080"),
		CacheDir:            getEnv("CACHE_DIR", "./cache"),
		CacheEnabled:        getEnv("CACHE_ENABLED", "true") == "true", // 默认启用缓存
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

	cache := NewFileCache(config.CacheDir)

	// 配置高性能的 Transport
	transport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		MaxConnsPerHost:       50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second, // 添加响应头超时
		DisableKeepAlives:     false,            // 启用 Keep-Alive

		// TLS 配置
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: false,
			MinVersion:         tls.VersionTLS12,
		},

		// 启用 HTTP/2
		ForceAttemptHTTP2: true,

		// 禁用压缩，让客户端直接处理
		DisableCompression: true,
	}

	return &ProxyServer{
		config:    config,
		cache:     cache,
		transport: transport,
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

		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
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

	// 检查缓存（如果启用）
	if p.config.CacheEnabled {
		cacheKey := p.generateCacheKey(r.Host, r.URL.Path)
		if p.isCacheable(r.URL.Path) {
			if cachedItem, found := p.cache.Get(cacheKey); found {
				if p.config.Debug {
					log.Printf("[DEBUG] /v2/* Cache HIT: %s", r.URL.Path)
				}
				p.serveCachedResponse(w, cachedItem)
				return
			}
			if p.config.Debug {
				log.Printf("[DEBUG] /v2/* Cache MISS: %s", r.URL.Path)
			}
		}
	}

	// 转发请求
	upstreamURL, _ := url.Parse(upstream + r.URL.Path)
	upstreamURL.RawQuery = r.URL.RawQuery

	p.proxyRequestWithRoundTrip(w, r, upstreamURL, true)
}

// 使用 RoundTrip 进行底层代理控制
func (p *ProxyServer) proxyRequestWithRoundTrip(w http.ResponseWriter, r *http.Request, targetURL *url.URL, enableCache bool) {
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
				// 使用黑名单机制决定如何处理重定向
				if p.isBlockedHost(redirectURL.Host) {
					// 黑名单中的域名:服务器端处理重定向
					// 原因: 这些域名被墙，客户端无法直接访问
					if p.config.Debug {
						log.Printf("[DEBUG] Blocked host detected (%s), following redirect server-side", redirectURL.Host)
					}
					// 使用 GET 方法跟随重定向,不带原始请求体和认证头
					// 这样可以保持签名 URL 的完整性 (对于外部存储的签名 URL)
					p.followRedirectWithSignedURL(w, redirectURL)
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

	shouldCache := p.config.CacheEnabled && enableCache && p.isCacheable(r.URL.Path)

	if shouldCache {
		cacheKey := p.generateCacheKey(r.Host, r.URL.Path)
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

// 跟随签名 URL 重定向 (用于 AWS S3/Cloudflare R2 等外部存储)
func (p *ProxyServer) followRedirectWithSignedURL(w http.ResponseWriter, signedURL *url.URL) {
	if p.config.Debug {
		log.Printf("[DEBUG] Following signed URL: %s", signedURL.String())
	}

	// 创建新的 GET 请求,不带原始请求的认证信息
	req, err := http.NewRequest("GET", signedURL.String(), nil)
	if err != nil {
		if p.config.Debug {
			log.Printf("[DEBUG] Failed to create signed URL request: %v", err)
		}
		p.writeErrorResponse(w, fmt.Sprintf("invalid signed URL: %v", err), http.StatusBadGateway)
		return
	}

	// 只设置必要的请求头
	req.Header.Set("User-Agent", "go-docker-proxy/1.0")
	// 不设置 Authorization 等认证头,因为签名 URL 本身包含认证信息

	// 使用 RoundTrip 执行请求
	resp, err := p.transport.RoundTrip(req)
	if err != nil {
		if p.config.Debug {
			log.Printf("[DEBUG] Signed URL request error: %v", err)
		}
		p.writeErrorResponse(w, fmt.Sprintf("signed URL request failed: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if p.config.Debug {
		log.Printf("[DEBUG] Signed URL response status: %d", resp.StatusCode)
	}

	// 如果返回 400/403,说明签名问题,记录详细错误
	if resp.StatusCode == http.StatusBadRequest || resp.StatusCode == http.StatusForbidden {
		if p.config.Debug {
			bodyBytes, _ := io.ReadAll(resp.Body)
			log.Printf("[DEBUG] Signed URL error response: %s", string(bodyBytes))
		}
	}

	// 直接返回响应,不缓存签名 URL 的结果(因为 URL 有时效性)
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

// 专门为 RoundTrip 优化的响应复制
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

	// 使用缓冲复制提高性能
	if resp.Body != nil {
		buf := make([]byte, 32*1024) // 32KB 缓冲区
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				if _, writeErr := w.Write(buf[:n]); writeErr != nil {
					return
				}
			}
			if err != nil {
				break
			}
		}
	}
}

// 带缓存的响应复制（RoundTrip版本）
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

	if !shouldStore || resp.StatusCode != http.StatusOK {
		w.WriteHeader(resp.StatusCode)
		if _, err := io.Copy(w, resp.Body); err != nil {
			fmt.Printf("proxy copy error: %v\n", err)
		}
		return
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		w.WriteHeader(resp.StatusCode)
		if len(bodyBytes) > 0 {
			_, _ = w.Write(bodyBytes)
		}
		fmt.Printf("proxy cache read error: %v\n", err)
		return
	}

	// 验证响应内容：只缓存有效的响应（Content-Length > 0）
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
	if len(bodyBytes) > 0 {
		_, _ = w.Write(bodyBytes)
	}

	go p.cache.Set(cacheKey, bodyBytes, headersToCache, resp.StatusCode, 1*time.Hour)
}

func (p *ProxyServer) serveCachedResponse(w http.ResponseWriter, item *CacheItem) {
	for key, values := range item.Headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.Header().Set("X-Cache", "HIT")
	w.WriteHeader(item.StatusCode)
	if len(item.Data) > 0 {
		_, _ = w.Write(item.Data)
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

func (p *ProxyServer) generateCacheKey(host, path string) string {
	return fmt.Sprintf("%s%s", host, path)
}

func (p *ProxyServer) isCacheable(path string) bool {
	return strings.Contains(path, "/manifests/") ||
		strings.Contains(path, "/blobs/sha256:")
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
