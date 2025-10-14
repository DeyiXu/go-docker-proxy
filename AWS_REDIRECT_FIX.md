# AWS S3 重定向问题修复说明

## 问题描述

在生产环境中发现 Docker Hub 镜像拉取失败,错误信息:
```
Error response from daemon: unknown: Missing x-amz-content-sha256
```

## 根本原因

1. **Docker Hub 的存储架构**: Docker Hub 将 blob 数据存储在 CDN 和云存储上
2. **重定向机制**: 当请求 blob 时,Docker Hub 返回重定向到 CDN 或 S3 URL
3. **原有问题**: 
   - 代理尝试跟随 S3 重定向但缺少 AWS 签名头 → 400 错误
   - Docker Hub CDN (`production.cloudflare.docker.com`) 在国内被墙 → 客户端无法访问

## 错误流程

```
Docker 客户端 → Proxy → Docker Hub
                         ↓ 301/307 重定向
                      Location: https://production.cloudflare.docker.com/...
                         ↓
                    Proxy 跟随重定向
                         ↓
                    AWS S3 (400 Bad Request)
                    错误: Missing x-amz-content-sha256
```

## 解决方案

**智能区分 Docker Hub CDN 和外部存储,采用不同的处理策略**

### 修复后的流程

#### 场景 1: Docker Hub CDN (国内被墙)
```
Docker 客户端 → Proxy → Docker Hub
                         ↓ 307 重定向
                    Location: production.cloudflare.docker.com
                         ↓
                    Proxy 检测到 Docker Hub CDN
                         ↓
                    Proxy 跟随重定向并下载
                         ↓
                    返回数据给客户端
                         ↓ 200 OK
                    下载成功 ✓
```

#### 场景 2: 外部存储 (AWS S3)
```
Docker 客户端 → Proxy → Docker Hub
                         ↓ 301 重定向
                    Location: amazonaws.com/...
                         ↓
                    Proxy 检测到外部存储
                         ↓
                    直接返回 301 给客户端
                         ↓
Docker 客户端 → AWS S3 (直接下载)
                         ↓ 200 OK
                    下载成功 ✓
```

## 技术实现

### 1. 支持所有重定向状态码
- 301 Moved Permanently
- 302 Found
- 303 See Other
- 307 Temporary Redirect
- 308 Permanent Redirect

### 2. 智能检测重定向目标
自动识别不同类型的重定向:

**Docker Hub CDN** (代理服务器跟随):
- `*.cloudflare.docker.com` - Docker Hub CDN (国内被墙)
- `*.docker.com` - Docker Hub 相关域名
- `*.docker.io` - Docker Hub 注册表

**外部存储** (返回给客户端):
- `*.amazonaws.com` - AWS S3
- `*.cloudfront.net` - AWS CloudFront
- `*.storage.googleapis.com` - Google Cloud Storage
- `*.blob.core.windows.net` - Azure Blob Storage

### 3. 区分处理策略

| 重定向类型 | 域名示例 | 处理方式 | 原因 |
|----------|---------|---------|------|
| Docker Hub CDN | production.cloudflare.docker.com | 服务器跟随 | 国内被墙,需代理 |
| Docker Hub 相关 | *.docker.io, *.docker.com | 服务器跟随 | 可能被墙 |
| AWS S3 | *.amazonaws.com | 返回给客户端 | 全球可达 |
| CloudFront | *.cloudfront.net | 返回给客户端 | CDN全球可达 |
| Google Cloud | *.storage.googleapis.com | 返回给客户端 | 全球可达 |
| 其他重定向 | 未知域名 | 服务器跟随 | 安全策略 |

## 优势

### 1. 避免复杂的 AWS 签名
- 无需实现 AWS Signature V4
- 无需处理 `x-amz-content-sha256` 等头
- 减少潜在的兼容性问题

### 2. 性能优化
- 客户端直接从 CDN/S3 下载,速度更快
- 减少代理服务器带宽压力
- 降低延迟

### 3. 符合标准
- Docker Registry V2 API 标准行为
- Docker 客户端原生支持重定向
- 与官方 Docker Hub 行为一致

### 4. 更好的扩展性
- 自动支持新的外部存储服务
- 预签名 URL 由源服务器管理
- 减少代理服务器的责任范围

## 测试方法

### 使用测试脚本
```bash
./test-aws-redirect.sh registry.w4w.cc:8080
```

### 手动测试
```bash
# 1. 测试 manifest
curl -I http://registry.w4w.cc:8080/v2/nginx/manifests/latest

# 2. 测试 blob 重定向
curl -I http://registry.w4w.cc:8080/v2/nginx/blobs/sha256:xxx

# 3. 完整拉取
docker pull registry.w4w.cc:8080/nginx:latest
```

## 相关文件

- `main.go` - 重定向处理逻辑
- `ARCHITECTURE.md` - 架构文档中的重定向章节
- `CHANGELOG.md` - 版本 v1.1.0 更新日志
- `test-aws-redirect.sh` - 自动化测试脚本

## 影响范围

### 受影响的镜像仓库
- ✅ Docker Hub (registry-1.docker.io)
- ✅ Quay.io (可能使用 AWS S3)
- ✅ 其他使用外部对象存储的 registry

### 不受影响的场景
- Manifest 请求 (无重定向)
- 小型 blob (可能不重定向)
- 私有 registry (取决于其实现)

## 参考资料

- [Docker Registry HTTP API V2](https://docs.docker.com/registry/spec/api/)
- [AWS Signature Version 4](https://docs.aws.amazon.com/general/latest/gr/signature-version-4.html)
- [RFC 7231 - HTTP/1.1 Redirections](https://tools.ietf.org/html/rfc7231#section-6.4)
