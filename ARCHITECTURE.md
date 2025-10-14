# Go Docker Proxy 架构设计

## 概述

Go Docker Proxy 是 [ciiiii/cloudflare-docker-proxy](https://github.com/ciiiii/cloudflare-docker-proxy) 的 Go 语言实现，提供完全兼容的 Docker 镜像代理服务，并增加了文件缓存功能。

## 核心设计原则

1. **完全兼容性**: 与 ciiiii/cloudflare-docker-proxy 的路由规则和行为完全一致
2. **性能优化**: 使用 Go 的 `http.Transport.RoundTrip` 实现高性能代理
3. **专用缓存**: 专为 Docker Registry 设计的两层缓存系统（内存索引 + 磁盘存储）
4. **可观测性**: 提供详细的调试日志和缓存统计

## 架构组件

### 1. 路由系统

```
请求 → 路由匹配 → 上游选择
  │       │           │
  │       │           └─→ Docker Hub / Quay / GCR / ...
  │       │
  │       └─→ 基于 Host Header 匹配
  │
  └─→ docker.{domain} / quay.{domain} / ...
```

**实现细节**:
- 使用 `map[string]string` 存储域名到上游的映射
- 从 Host header 中提取域名（移除端口）
- 调试模式支持 `TARGET_UPSTREAM` 作为后备

### 2. 认证流程

```
客户端请求
    │
    ├─→ /v2/ (检查认证)
    │   │
    │   ├─→ 401 → 返回认证挑战
    │   └─→ 200 → 继续
    │
    ├─→ /v2/auth (获取 Token)
    │   │
    │   ├─→ 请求上游 /v2/
    │   ├─→ 解析 WWW-Authenticate
    │   ├─→ 处理 Docker Hub scope
    │   └─→ 获取并返回 Token
    │
    └─→ /v2/{repo}/... (代理请求)
        │
        └─→ 使用 Token 访问上游
```

**Docker Hub 特殊处理**:
- Library 镜像重定向: `/v2/nginx/...` → `/v2/library/nginx/...`
- Scope 自动补全: `repository:nginx:pull` → `repository:library/nginx:pull`

### 3. 缓存系统

专为 Docker Registry 设计的两层缓存架构：

```
请求 → 内存索引查找
  │        │
  │        ├─→ 命中 → 检查过期 → 返回 (X-Cache: HIT)
  │        │
  │        └─→ 未命中 → 磁盘查找
  │                   │
  │                   ├─→ 找到 → 加载到内存 → 返回 (X-Cache: HIT)
  │                   │
  │                   └─→ 未找到 → 请求上游
  │                               │
  │                               ├─→ 异步保存到磁盘
  │                               ├─→ 更新内存索引
  │                               └─→ 返回响应 (X-Cache: MISS)
```

**缓存特性**:

1. **两层架构**:
   - **内存层**: 使用 `map[string]*CacheItem` 快速索引
   - **磁盘层**: 分类存储（manifests/ 和 blobs/）

2. **智能 TTL**:
   - **Manifest**: 1小时（频繁更新）
   - **Blob**: 7天（不可变内容）
   - **其他**: 使用调用者指定的 TTL

3. **目录分层**:
   ```
   cache/
   ├── manifests/        # Manifest 缓存
   │   ├── ab/           # 第一层目录（hash前2位）
   │   │   └── cd/       # 第二层目录（hash第3-4位）
   │   │       ├── abcd1234...      # 数据文件
   │   │       └── abcd1234....meta # 元数据文件
   │   └── ...
   └── blobs/            # Blob 缓存
       └── ...
   ```

4. **Hash算法**: 使用 SHA256 替代 MD5（更安全）
   ```go
   cacheKey = host + path
   hash = sha256(cacheKey)
   filePath = {manifests|blobs}/{hash[0:2]}/{hash[2:4]}/{hash}
   ```

5. **元数据结构**:
   ```json
   {
     "headers": {...},
     "statusCode": 200,
     "expiresAt": 1697270400,
     "cachedAt": 1697266800,
     "size": 1024,
     "contentType": "application/vnd.docker.distribution.manifest.v2+json"
   }
   ```

6. **并发安全**:
   - 读写锁保护内存索引
   - 原子操作更新统计信息
   - 异步磁盘 I/O，不阻塞请求

7. **后台清理**:
   - 每30分钟扫描过期项
   - 自动删除过期的内存和磁盘数据
   - 更新统计信息

8. **统计监控**:
   ```go
   stats := cache.GetStats()
   // 输出: Hits, Misses, TotalSize, ItemCount, LastCleanup
   ```

### 4. 请求代理

使用 `http.Transport.RoundTrip` 实现底层代理：

```go
// 创建代理请求
req := createProxyRequest(originalReq, targetURL)

// 执行请求
resp, err := transport.RoundTrip(req)

// 处理响应
copyResponseRoundTrip(w, resp)
```

**优势**:
- 完全控制请求/响应
- 避免自动跟随重定向（Docker Hub blob 需要手动处理）
- 精确的头部控制
- 更好的性能

### 5. Docker Hub 重定向处理

Docker Hub 对 blob 请求返回 307 重定向到 S3/CloudFront:

```
客户端 → Proxy → Docker Hub (307)
                    ↓
                Location: https://cloudfront.net/...
                    ↓
         Proxy → CloudFront (200)
                    ↓
         返回给客户端
```

**实现**:
```go
if isDockerHub && resp.StatusCode == http.StatusTemporaryRedirect {
    location := resp.Header.Get("Location")
    redirectURL, _ := url.Parse(location)
    // 递归代理到重定向地址
    proxyRequestWithRoundTrip(w, r, redirectURL, enableCache)
}
```

## 与 Cloudflare Worker 版本的差异

| 特性 | Cloudflare Worker | Go 实现 |
|------|-------------------|---------|
| 运行环境 | Edge (Workers) | 任意服务器 |
| 缓存 | Workers KV (分布式) | 磁盘文件 (本地) |
| 冷启动 | 极快 (~0ms) | 需要启动 (~100ms) |
| 扩展性 | 全球自动扩展 | 需要手动负载均衡 |
| 成本 | 按请求计费 | 固定服务器成本 |
| 调试 | 有限 | 完整日志 |
| 定制化 | JavaScript | Go (更灵活) |

## 性能优化

### 1. Transport 配置
```go
transport := &http.Transport{
    MaxIdleConns:          100,  // 最大空闲连接
    MaxIdleConnsPerHost:   20,   // 每个主机的最大空闲连接
    MaxConnsPerHost:       50,   // 每个主机的最大连接
    IdleConnTimeout:       90s,  // 空闲连接超时
    TLSHandshakeTimeout:   10s,  // TLS 握手超时
    ForceAttemptHTTP2:     true, // 启用 HTTP/2
    DisableCompression:    true, // 禁用压缩（由客户端处理）
}
```

### 2. 缓冲区大小
```go
buf := make([]byte, 32*1024)  // 32KB 缓冲区
```

### 3. 异步缓存写入
```go
go cache.Set(key, data, headers, status, ttl)  // 异步保存
```

## 监控和调试

### 调试日志
启用 `DEBUG=true` 后，将输出：
- 请求路由决策
- 缓存命中/未命中
- 上游响应状态
- 重定向处理
- Token 获取过程
- 认证挑战

### 健康检查
```bash
GET /health
GET /healthz
```

返回:
```json
{
  "status": "healthy",
  "timestamp": "2024-10-14T10:00:00Z",
  "version": "1.0.0",
  "uptime": "1h30m45s"
}
```

## 部署建议

### 1. 生产环境
- 使用 Nginx/Caddy 反向代理
- 启用 HTTPS
- 配置日志轮转
- 监控缓存目录大小
- 定期备份缓存（可选）

### 2. 高可用
- 使用负载均衡器（HAProxy/Nginx）
- 部署多个实例
- 共享存储或独立缓存（推荐独立）

### 3. 性能调优
- 增大缓存目录空间
- 使用 SSD 存储
- 调整 Transport 参数
- 监控内存使用

## 缓存设计亮点

### 为什么不使用现成的缓存库？

我们设计了专门针对 Docker Registry 的缓存系统，而不是使用通用缓存库，原因如下：

1. **内容特定优化**:
   - Manifest 和 Blob 有不同的生命周期特征
   - Manifest 频繁变更，需要短 TTL
   - Blob 是不可变的，可以长期缓存

2. **目录结构优化**:
   - 分离 manifests 和 blobs 便于管理
   - 使用 SHA256 hash 确保唯一性
   - 分层目录避免单目录文件过多（性能问题）

3. **两层缓存策略**:
   - 内存索引提供快速查找
   - 磁盘存储提供持久化
   - 懒加载策略节省内存

4. **统计和监控**:
   - 内置缓存命中率统计
   - 磁盘空间使用监控
   - 清理操作可视化

5. **性能优先**:
   - 异步磁盘 I/O
   - 原子操作减少锁竞争
   - 读写分离提高并发

## 参考资料

- [Docker Registry V2 API](https://docs.docker.com/registry/spec/api/)
- [ciiiii/cloudflare-docker-proxy](https://github.com/ciiiii/cloudflare-docker-proxy)
- [OCI Distribution Specification](https://github.com/opencontainers/distribution-spec)
