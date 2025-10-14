# 🔥 紧急修复: Docker Hub CDN 被墙问题

## 问题描述

用户报告: `production.cloudflare.docker.com` 在国内被墙,导致即使代理返回重定向,客户端也无法访问。

```
❌ 错误流程 (v1.1.0 初版):
Docker Client → Proxy → Docker Hub
                         ↓ 307 redirect
                   Location: production.cloudflare.docker.com
                         ↓
                   返回 307 给客户端
                         ↓
Docker Client → production.cloudflare.docker.com
                         ↓
                   连接超时 (被墙) ❌
```

## 修复方案

**区分 Docker Hub CDN 和外部存储,采用不同策略:**

### 1. Docker Hub CDN (代理跟随)
```
✅ 正确流程:
Docker Client → Proxy → Docker Hub
                         ↓ 307 redirect
                   Location: production.cloudflare.docker.com
                         ↓
                   Proxy 跟随并下载
                         ↓
                   返回数据给客户端
                         ↓
                   下载成功 ✅
```

**识别规则**:
- `*.cloudflare.docker.com`
- `*.docker.com`
- `*.docker.io`

**原因**: 这些域名可能在国内被墙,需要代理服务器帮助下载

### 2. 外部存储 (返回客户端)
```
✅ 正确流程:
Docker Client → Proxy → Docker Hub
                         ↓ 301 redirect
                   Location: s3.amazonaws.com/...
                         ↓
                   返回 301 给客户端
                         ↓
Docker Client → AWS S3 (直接下载)
                         ↓
                   下载成功 ✅
```

**识别规则**:
- `*.amazonaws.com` (AWS S3)
- `*.cloudfront.net` (AWS CloudFront)
- `*.storage.googleapis.com` (Google Cloud Storage)
- `*.blob.core.windows.net` (Azure Blob Storage)

**原因**: 这些是全球 CDN,客户端可以直接访问,性能更好

### 3. 其他重定向 (代理跟随)
未知域名默认由代理跟随,确保可靠性。

---

## 代码变更

### 修改前
```go
// 只检测外部存储,返回给客户端
isExternalStorage := strings.Contains(redirectURL.Host, "amazonaws.com") ||
    strings.Contains(redirectURL.Host, "cloudfront.net") ||
    // ...

if isExternalStorage {
    // 返回重定向给客户端
    p.copyResponseRoundTrip(w, resp)
    return
}
```

### 修改后
```go
// 优先检测 Docker Hub CDN - 代理跟随
isDockerHubCDN := strings.Contains(redirectURL.Host, "cloudflare.docker.com") ||
    strings.Contains(redirectURL.Host, "docker.com") ||
    strings.Contains(redirectURL.Host, "docker.io")

if isDockerHubCDN {
    // 代理服务器跟随重定向
    p.proxyRequestWithRoundTrip(w, r, redirectURL, enableCache)
    return
}

// 检测外部存储 - 返回给客户端
isExternalStorage := strings.Contains(redirectURL.Host, "amazonaws.com") ||
    strings.Contains(redirectURL.Host, "cloudfront.net") ||
    // ...

if isExternalStorage {
    // 返回重定向给客户端
    p.copyResponseRoundTrip(w, resp)
    return
}

// 其他重定向 - 代理跟随
p.proxyRequestWithRoundTrip(w, r, redirectURL, enableCache)
return
```

---

## 影响分析

### 优点
✅ **解决国内访问问题**: Docker Hub CDN 被代理,国内用户可正常下载  
✅ **保持性能优势**: 外部存储仍返回客户端,利用全球 CDN  
✅ **提升可靠性**: 未知重定向默认代理,避免失败  
✅ **灵活扩展**: 可轻松添加更多需要代理的域名

### 潜在影响
⚠️ **带宽消耗**: 代理 Docker Hub CDN 会增加服务器带宽  
⚠️ **延迟增加**: 经过代理会比直连 CDN 慢一些  
✅ **可接受**: 相比无法下载,这些影响可以接受

---

## 测试验证

### 启用 DEBUG 模式
```bash
export DEBUG=true
./go-docker-proxy
```

### 查看日志
```bash
# 应该看到类似输出:
[DEBUG] Proxy got redirect 307 to: https://production.cloudflare.docker.com/...
[DEBUG] Docker Hub CDN detected (production.cloudflare.docker.com), following redirect server-side
[DEBUG] Proxy request to: https://production.cloudflare.docker.com/...
[DEBUG] Proxy response status: 200 from production.cloudflare.docker.com
```

### 测试拉取
```bash
# 清理旧镜像
docker rmi registry.w4w.cc:8080/nginx:latest

# 拉取测试
docker pull registry.w4w.cc:8080/nginx:latest

# 应该成功下载 ✓
```

---

## 部署建议

### 立即部署
```bash
# 1. 拉取代码
git pull origin main

# 2. 重新编译
go build -o go-docker-proxy

# 3. 重启服务
docker-compose restart
# 或
sudo systemctl restart go-docker-proxy
```

### 验证
```bash
# 测试拉取
docker pull registry.w4w.cc:8080/nginx:latest

# 查看日志
docker-compose logs -f | grep "Docker Hub CDN"
```

---

## 性能考虑

### 带宽估算
假设:
- 平均镜像大小: 100MB
- 日均拉取: 100 次
- **日均带宽**: 10GB
- **月均带宽**: 300GB

### 优化建议
1. **启用缓存**: Docker Hub CDN 的内容可以缓存
2. **限流控制**: 必要时可添加限流
3. **监控告警**: 监控带宽和流量
4. **CDN 加速**: 考虑在代理前加一层 CDN

---

## 相关文档

- [AWS_REDIRECT_FIX.md](./AWS_REDIRECT_FIX.md) - 详细技术说明
- [CHANGELOG.md](./CHANGELOG.md) - v1.1.0 版本记录
- [DEBUG_LOGGING.md](./DEBUG_LOGGING.md) - 调试日志使用指南

---

## 总结

这个修复至关重要,因为:

1. **解决了核心问题**: 国内用户无法拉取 Docker Hub 镜像
2. **保持了性能**: 外部存储仍走直连
3. **提升了可靠性**: 默认代理未知重定向
4. **易于维护**: 清晰的分类逻辑

**立即部署到生产环境,解决用户痛点!** 🚀

---

**修复版本**: v1.1.0  
**提交**: 2031f83  
**状态**: ✅ 已修复并推送
