# Cloudflare R2 存储重定向问题修复

## 问题描述

### 错误信息
```
error pulling image configuration: download failed after attempts=6: 
unknown: <?xml version="1.0" encoding="UTF-8"?>
<Error>
  <Code>InvalidRequest</Code>
  <Message>Missing x-amz-content-sha256</Message>
</Error>
```

### 错误日志
```
[DEBUG] Proxy response status: 307 from registry-1.docker.io
[DEBUG] Proxy got redirect 307 to: https://docker-images-prod.*.r2.cloudflarestorage.com/...
[DEBUG] Following redirect to: docker-images-prod.*.r2.cloudflarestorage.com
[DEBUG] Proxy request to: https://docker-images-prod.*.r2.cloudflarestorage.com/...
[DEBUG] Proxy response status: 400 from docker-images-prod.*.r2.cloudflarestorage.com
```

### 问题分析

1. **Docker Hub 使用 Cloudflare R2 存储 blob 数据**
   - R2 是 Cloudflare 的对象存储服务,兼容 AWS S3 API
   - 域名格式: `docker-images-prod.*.r2.cloudflarestorage.com`

2. **代理跟随重定向导致问题**
   - Docker Hub 返回 307 重定向到 R2 存储 (带 AWS 签名的 URL)
   - 代理服务器尝试跟随重定向
   - 代理重新发起请求时,原始 URL 中的 AWS 签名失效
   - R2 要求 `x-amz-content-sha256` 头,但代理请求中缺少

3. **AWS 签名机制**
   - R2 URL 包含时间敏感的签名参数:
     - `X-Amz-Algorithm`
     - `X-Amz-Credential`
     - `X-Amz-Date`
     - `X-Amz-Expires`
     - `X-Amz-Signature`
   - 签名基于原始请求的完整信息(包括请求头)计算
   - 代理服务器重新发起请求会改变请求特征,导致签名验证失败

## 解决方案

### 修复策略

**服务器端跟随签名 URL,使用正确的 HTTP 方法和请求头**

关键要点:
1. **识别外部存储域名** - 检测 R2/S3 等签名 URL
2. **使用 GET 方法** - 不使用原始请求的方法(可能是 HEAD)
3. **不带原始请求体** - 签名 URL 请求不需要请求体
4. **不带认证头** - 签名 URL 本身包含认证信息
5. **保持 URL 完整性** - 不修改任何 URL 参数

```go
// 外部存储检测规则
isExternalStorage := strings.Contains(redirectURL.Host, "amazonaws.com") ||
    strings.Contains(redirectURL.Host, "cloudfront.net") ||
    strings.Contains(redirectURL.Host, "cloudflarestorage.com") || // Cloudflare R2
    strings.Contains(redirectURL.Host, "storage.googleapis.com") ||
    strings.Contains(redirectURL.Host, "blob.core.windows.net")

if isExternalStorage {
    // 使用专门的函数处理签名 URL
    p.followRedirectWithSignedURL(w, redirectURL, enableCache)
    return
}
```

**新增 followRedirectWithSignedURL 函数:**
```go
func (p *ProxyServer) followRedirectWithSignedURL(w http.ResponseWriter, signedURL *url.URL, enableCache bool) {
    // 创建新的 GET 请求
    req, _ := http.NewRequest("GET", signedURL.String(), nil)
    
    // 只设置必要的请求头
    req.Header.Set("User-Agent", "go-docker-proxy/1.0")
    // 不设置 Authorization 等认证头
    
    // 执行请求
    resp, _ := p.transport.RoundTrip(req)
    defer resp.Body.Close()
    
    // 直接返回响应(不缓存,因为 URL 有时效性)
    p.copyResponseRoundTrip(w, resp)
}
```

### 修复后的流程

```
Docker 客户端 → Proxy → Docker Hub (registry-1.docker.io)
                         ↓ 307 Temporary Redirect
                    Location: docker-images-prod.*.r2.cloudflarestorage.com/...
                    (带完整 AWS 签名参数)
                         ↓
                    Proxy 检测到 Cloudflare R2 存储
                         ↓
                    Proxy 创建新的 GET 请求
                    - 使用完整的签名 URL
                    - 不带原始请求体
                    - 不带 Authorization 头
                    - 只带基本的 User-Agent
                         ↓
Proxy → Cloudflare R2 (使用签名 URL)
                         ↓ 200 OK
                    Proxy 接收数据
                         ↓
                    返回数据给客户端
                         ↓ 200 OK
                    下载成功 ✓
```

## 代码变更

### 变更前 (错误的方式)
```go
// 方式1: 返回重定向给客户端 - 客户端可能无法访问
p.copyResponseRoundTrip(w, resp)

// 方式2: 直接用 proxyRequestWithRoundTrip - 会破坏签名
p.proxyRequestWithRoundTrip(w, r, redirectURL, enableCache)
// 问题: 使用了原始请求的方法、请求体、认证头
```

### 变更后 (正确的方式)
```go
// 1. 检测外部存储
isExternalStorage := strings.Contains(redirectURL.Host, "amazonaws.com") ||
    strings.Contains(redirectURL.Host, "cloudfront.net") ||
    strings.Contains(redirectURL.Host, "cloudflarestorage.com") || // Cloudflare R2
    strings.Contains(redirectURL.Host, "storage.googleapis.com") ||
    strings.Contains(redirectURL.Host, "blob.core.windows.net")

if isExternalStorage {
    // 2. 使用专门的函数处理签名 URL
    p.followRedirectWithSignedURL(w, redirectURL, enableCache)
    return
}

// 3. 新函数实现
func (p *ProxyServer) followRedirectWithSignedURL(w http.ResponseWriter, signedURL *url.URL, enableCache bool) {
    // 创建干净的 GET 请求
    req, _ := http.NewRequest("GET", signedURL.String(), nil)
    req.Header.Set("User-Agent", "go-docker-proxy/1.0")
    // 关键: 不设置 Authorization 等认证头
    
    resp, _ := p.transport.RoundTrip(req)
    defer resp.Body.Close()
    
    // 直接返回响应
    p.copyResponseRoundTrip(w, resp)
}
```

## 影响分析

### 优点
1. ✅ **修复 R2 存储下载失败** - 正确处理 AWS 签名请求
2. ✅ **服务器端处理** - 代理服务器完全控制流量
3. ✅ **统一流量管理** - 所有流量经过代理,便于监控和计费
4. ✅ **客户端无感知** - 客户端不需要能够直接访问 R2
5. ✅ **保持签名完整性** - 使用正确的请求方法和头部
6. ✅ **零配置** - 无需额外配置 AWS 凭据
7. ✅ **向后兼容** - 不影响其他类型的重定向

### 关键改进
- ✅ **使用 GET 方法** - 不使用原始请求的 HEAD/其他方法
- ✅ **不带认证头** - 签名 URL 本身包含认证信息
- ✅ **不带请求体** - 避免干扰签名验证
- ✅ **完整 URL** - 保留所有签名参数

### 潜在影响
1. ⚠️ **带宽消耗** - 所有 blob 流量经过代理服务器
2. ⚠️ **延迟增加** - 相比客户端直连 CDN,会有轻微延迟
3. ⚠️ **无缓存** - R2 签名 URL 有时效性,不适合长期缓存

### 适用场景
- ✅ 需要统一流量管理和监控
- ✅ 客户端网络受限,无法直接访问 R2
- ✅ 需要隐藏客户端 IP
- ✅ 境外服务器,网络到 R2 速度快

### 性能考虑
- 代理服务器需要有足够带宽处理所有 blob 下载
- 建议部署在网络条件好的区域(如亚太)
- 可以考虑添加限流控制

## 测试验证

### 测试步骤

1. **拉取镜像**
   ```bash
   docker pull registry.w4w.cc:8080/nginx:latest
   ```

2. **检查日志** (DEBUG=true)
   ```bash
   # 应该看到:
   [DEBUG] Proxy got redirect 307 to: https://docker-images-prod.*.r2.cloudflarestorage.com/...
   [DEBUG] External storage detected (*.r2.cloudflarestorage.com), returning redirect to client
   ```

3. **验证成功**
   ```bash
   docker images | grep nginx
   # 应该看到成功下载的镜像
   ```

### 测试结果预期

- ✅ 镜像拉取成功,无 400 错误
- ✅ 日志显示 R2 重定向被正确识别
- ✅ 客户端直接从 R2 下载 blob 数据

## 部署建议

### 更新步骤

1. **拉取最新代码**
   ```bash
   cd /path/to/go-docker-proxy
   git pull origin main
   ```

2. **重新编译**
   ```bash
   go build -o go-docker-proxy
   ```

3. **重启服务**
   ```bash
   # Docker Compose
   docker-compose down && docker-compose up -d --build

   # 或直接重启容器
   docker-compose restart
   ```

4. **验证修复**
   ```bash
   # 启用 DEBUG 模式测试
   docker-compose logs -f go-docker-proxy
   
   # 在另一个终端拉取镜像
   docker pull registry.your-domain.com/nginx:latest
   ```

### 回滚方案

如果遇到问题,可以回滚到之前的版本:

```bash
# 查看提交历史
git log --oneline

# 回滚到之前的提交
git checkout <previous-commit-hash>

# 重新编译和部署
go build -o go-docker-proxy
docker-compose restart
```

## 相关文档

- [AWS_REDIRECT_FIX.md](./AWS_REDIRECT_FIX.md) - AWS S3 重定向完整说明
- [HOTFIX_CDN.md](./HOTFIX_CDN.md) - Docker Hub CDN 被墙问题修复
- [CHANGELOG.md](./CHANGELOG.md) - 完整更新日志

## 技术参考

### Cloudflare R2 文档
- [R2 Storage](https://developers.cloudflare.com/r2/)
- [AWS S3 Compatibility](https://developers.cloudflare.com/r2/api/s3/)
- [Pre-signed URLs](https://developers.cloudflare.com/r2/api/s3/presigned-urls/)

### AWS Signature V4
- [Signing AWS Requests](https://docs.aws.amazon.com/general/latest/gr/signing_aws_api_requests.html)
- [Signature Version 4](https://docs.aws.amazon.com/general/latest/gr/signature-version-4.html)

## 总结

此修复通过识别 Cloudflare R2 存储域名,将其归类为外部存储,从而避免代理服务器跟随重定向破坏 AWS 签名的问题。这是一个简单但有效的解决方案,无需实现复杂的 AWS Signature V4 签名机制。

**关键点:**
- 🎯 识别 `cloudflarestorage.com` 域名
- 🎯 保持 AWS 签名 URL 完整性
- 🎯 让客户端直接访问 R2 CDN
- 🎯 零配置,即插即用
