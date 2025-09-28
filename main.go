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
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

type Config struct {
	Port         string
	CacheDir     string
	Debug        bool
	CustomDomain string
	Routes       map[string]string
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

	config := &Config{
		Port:         getEnv("PORT", "8080"),
		CacheDir:     getEnv("CACHE_DIR", "./cache"),
		Debug:        getEnv("DEBUG", "false") == "true",
		CustomDomain: customDomain,
		Routes:       buildRoutes(customDomain),
	}

	cache := NewFileCache(config.CacheDir)

	// 配置高性能的 Transport
	transport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		MaxConnsPerHost:       50,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,

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

// 根据自定义域名构建路由映射，完全参考原版 cloudflare-docker-proxy
func buildRoutes(customDomain string) map[string]string {
	dockerHub := "https://registry-1.docker.io"

	routes := map[string]string{
		// production - 完全按照原版的命名规则
		fmt.Sprintf("registry.docker.%s", customDomain):            dockerHub,
		fmt.Sprintf("quay.registry.docker.%s", customDomain):       "https://quay.io",
		fmt.Sprintf("gcr.registry.docker.%s", customDomain):        "https://gcr.io",
		fmt.Sprintf("k8s-gcr.registry.docker.%s", customDomain):    "https://k8s.gcr.io",
		fmt.Sprintf("k8s.registry.docker.%s", customDomain):        "https://registry.k8s.io",
		fmt.Sprintf("ghcr.registry.docker.%s", customDomain):       "https://ghcr.io",
		fmt.Sprintf("cloudsmith.registry.docker.%s", customDomain): "https://docker.cloudsmith.io",
		fmt.Sprintf("ecr.registry.docker.%s", customDomain):        "https://public.ecr.aws",

		// staging
		fmt.Sprintf("docker-staging.registry.docker.%s", customDomain): dockerHub,
	}

	// 添加一些常用的简化域名
	if customDomain != "localhost" {
		routes[fmt.Sprintf("docker.%s", customDomain)] = dockerHub
		routes[fmt.Sprintf("hub.%s", customDomain)] = dockerHub
		routes[fmt.Sprintf("registry.%s", customDomain)] = dockerHub
	} else {
		// 本地开发时的简化配置
		routes["docker.localhost"] = dockerHub
		routes["hub.localhost"] = dockerHub
		routes["registry.localhost"] = dockerHub
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
		p.writeRoutesResponse(w)
		return
	}

	upstreamURL, _ := url.Parse(upstream + "/v2/")
	p.proxyRequestWithRoundTrip(w, r, upstreamURL, false)
}

func (p *ProxyServer) handleAuth(w http.ResponseWriter, r *http.Request) {
	upstream := p.routeByHost(r.Host)
	if upstream == "" {
		p.writeRoutesResponse(w)
		return
	}

	upstreamURL, _ := url.Parse(upstream + "/v2/")
	req := p.createProxyRequest(r, upstreamURL)
	req.Method = "GET"

	// 使用 RoundTrip 直接调用
	resp, err := p.transport.RoundTrip(req)
	if err != nil {
		p.writeErrorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		p.copyResponseRoundTrip(w, resp)
		return
	}

	authenticateStr := resp.Header.Get("WWW-Authenticate")
	if authenticateStr == "" {
		p.copyResponseRoundTrip(w, resp)
		return
	}

	wwwAuth, err := p.parseAuthenticate(authenticateStr)
	if err != nil {
		p.writeErrorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}

	scope := r.URL.Query().Get("scope")

	// 处理Docker Hub library镜像的scope
	if strings.Contains(upstream, "registry-1.docker.io") && scope != "" {
		scope = p.processDockerHubScope(scope)
	}

	token, err := p.fetchTokenWithRoundTrip(wwwAuth, scope, r.Header.Get("Authorization"))
	if err != nil {
		p.writeErrorResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer token.Body.Close()

	p.copyResponseRoundTrip(w, token)
}

func (p *ProxyServer) handleV2Request(w http.ResponseWriter, r *http.Request) {
	upstream := p.routeByHost(r.Host)
	if upstream == "" {
		p.writeRoutesResponse(w)
		return
	}

	isDockerHub := strings.Contains(upstream, "registry-1.docker.io")

	// 处理Docker Hub library镜像重定向
	if isDockerHub {
		if redirectURL := p.processDockerHubLibraryRedirect(r.URL.Path); redirectURL != "" {
			http.Redirect(w, r, redirectURL, http.StatusMovedPermanently)
			return
		}
	}

	// 检查缓存
	cacheKey := p.generateCacheKey(r.Host, r.URL.Path)
	if cachedData, found := p.cache.Get(cacheKey); found {
		if p.isCacheable(r.URL.Path) {
			p.serveCachedResponse(w, cachedData, r.URL.Path)
			return
		}
	}

	// 转发请求
	upstreamURL, _ := url.Parse(upstream + r.URL.Path)
	upstreamURL.RawQuery = r.URL.RawQuery

	p.proxyRequestWithRoundTrip(w, r, upstreamURL, true)
}

// 使用 RoundTrip 进行底层代理控制
func (p *ProxyServer) proxyRequestWithRoundTrip(w http.ResponseWriter, r *http.Request, targetURL *url.URL, enableCache bool) {
	// 创建代理请求
	req := p.createProxyRequest(r, targetURL)

	// 使用 RoundTrip 直接执行请求
	resp, err := p.transport.RoundTrip(req)
	if err != nil {
		p.writeErrorResponse(w, fmt.Sprintf("transport error: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// 处理认证
	if resp.StatusCode == http.StatusUnauthorized {
		p.responseUnauthorized(w, r)
		return
	}

	// 处理 Docker Hub 重定向 (状态码 307)
	if strings.Contains(targetURL.Host, "registry-1.docker.io") && resp.StatusCode == http.StatusTemporaryRedirect {
		location := resp.Header.Get("Location")
		if location != "" {
			redirectURL, err := url.Parse(location)
			if err != nil {
				p.writeErrorResponse(w, fmt.Sprintf("invalid redirect URL: %v", err), http.StatusBadGateway)
				return
			}
			p.proxyRequestWithRoundTrip(w, r, redirectURL, enableCache)
			return
		}
	}

	// 复制响应
	if enableCache {
		cacheKey := p.generateCacheKey(r.Host, r.URL.Path)
		p.copyResponseWithCacheRoundTrip(w, resp, cacheKey)
	} else {
		p.copyResponseRoundTrip(w, resp)
	}
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
	// 移除端口号
	if idx := strings.Index(host, ":"); idx != -1 {
		host = host[:idx]
	}

	if upstream, exists := p.config.Routes[host]; exists {
		return upstream
	}

	// 调试模式下的默认上游
	if p.config.Debug {
		if targetUpstream := getEnv("TARGET_UPSTREAM", ""); targetUpstream != "" {
			return targetUpstream
		}
	}

	return ""
}

func (p *ProxyServer) processDockerHubLibraryRedirect(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) == 5 && parts[1] == "v2" {
		parts = append(parts[:2], append([]string{"library"}, parts[2:]...)...)
		return strings.Join(parts, "/")
	}
	return ""
}

func (p *ProxyServer) processDockerHubScope(scope string) string {
	parts := strings.Split(scope, ":")
	if len(parts) == 3 && !strings.Contains(parts[1], "/") {
		parts[1] = "library/" + parts[1]
		return strings.Join(parts, ":")
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
func (p *ProxyServer) copyResponseWithCacheRoundTrip(w http.ResponseWriter, resp *http.Response, cacheKey string) {
	skipHeaders := map[string]bool{
		"Connection":        true,
		"Proxy-Connection":  true,
		"Upgrade":           true,
		"Transfer-Encoding": true,
	}

	for key, values := range resp.Header {
		if !skipHeaders[key] {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
	}

	w.WriteHeader(resp.StatusCode)

	if resp.Body != nil {
		if p.isCacheable(cacheKey) && resp.StatusCode == http.StatusOK {
			// 使用内存缓冲收集数据
			var allData []byte
			buf := make([]byte, 32*1024)

			for {
				n, err := resp.Body.Read(buf)
				if n > 0 {
					chunk := make([]byte, n)
					copy(chunk, buf[:n])
					allData = append(allData, chunk...)

					if _, writeErr := w.Write(chunk); writeErr != nil {
						return
					}
				}
				if err != nil {
					break
				}
			}

			// 异步缓存数据
			if len(allData) > 0 {
				go func() {
					p.cache.Set(cacheKey, allData, 1*time.Hour)
				}()
			}
		} else {
			// 直接复制，不缓存
			p.copyResponseRoundTrip(w, resp)
			return
		}
	}
}

func (p *ProxyServer) serveCachedResponse(w http.ResponseWriter, data []byte, path string) {
	contentType := p.getContentType(path)
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Cache", "HIT")
	w.WriteHeader(http.StatusOK)
	w.Write(data)
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

func (p *ProxyServer) getContentType(path string) string {
	if strings.Contains(path, "/manifests/") {
		return "application/vnd.docker.distribution.manifest.v2+json"
	}
	if strings.Contains(path, "/blobs/") {
		return "application/octet-stream"
	}
	return "application/json"
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
