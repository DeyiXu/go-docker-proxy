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

**将 Cloudflare R2 识别为外部存储,直接返回重定向给客户端**

```go
// 外部存储检测规则
isExternalStorage := strings.Contains(redirectURL.Host, "amazonaws.com") ||
    strings.Contains(redirectURL.Host, "cloudfront.net") ||
    strings.Contains(redirectURL.Host, "cloudflarestorage.com") || // 新增: Cloudflare R2
    strings.Contains(redirectURL.Host, "storage.googleapis.com") ||
    strings.Contains(redirectURL.Host, "blob.core.windows.net")

if isExternalStorage {
    // 直接返回重定向响应给客户端
    p.copyResponseRoundTrip(w, resp)
    return
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
                    直接返回 307 给客户端 (保留原始签名)
                         ↓
Docker 客户端 → Cloudflare R2 (直接下载,使用原始签名 URL)
                         ↓ 200 OK
                    下载成功 ✓
```

## 代码变更

### 变更前
```go
// 只检测传统云存储
isExternalStorage := strings.Contains(redirectURL.Host, "amazonaws.com") ||
    strings.Contains(redirectURL.Host, "cloudfront.net") ||
    strings.Contains(redirectURL.Host, "storage.googleapis.com") ||
    strings.Contains(redirectURL.Host, "blob.core.windows.net")
```

### 变更后
```go
// 增加 Cloudflare R2 检测,并添加注释说明签名问题
isExternalStorage := strings.Contains(redirectURL.Host, "amazonaws.com") ||
    strings.Contains(redirectURL.Host, "cloudfront.net") ||
    strings.Contains(redirectURL.Host, "cloudflarestorage.com") || // Cloudflare R2
    strings.Contains(redirectURL.Host, "storage.googleapis.com") ||
    strings.Contains(redirectURL.Host, "blob.core.windows.net")
```

## 影响分析

### 优点
1. ✅ **修复 R2 存储下载失败** - 客户端可以正常下载镜像
2. ✅ **保持签名完整性** - 不破坏 AWS 签名机制
3. ✅ **全球可达性** - Cloudflare R2 有良好的全球 CDN
4. ✅ **零配置** - 无需额外配置 AWS 凭据
5. ✅ **向后兼容** - 不影响其他类型的重定向

### 潜在影响
1. ⚠️ **网络要求** - 客户端需要能够直接访问 Cloudflare R2
2. ⚠️ **无缓存** - R2 重定向不会被代理缓存 (但 URL 有时效性,缓存意义不大)

### 适用场景
- ✅ 客户端可以访问全球 CDN
- ✅ 不需要代理隐藏客户端 IP
- ✅ 希望最优下载性能 (直连 CDN)

### 不适用场景
- ❌ 客户端网络严格受限,无法访问 Cloudflare
- ❌ 需要统一计费/审计所有流量

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
