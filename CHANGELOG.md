# 更新日志

## [v1.1.0] - 2024-12-XX

### 🔧 修复
- **重定向处理优化** - 修复 Docker Hub blob 下载失败和国内访问问题
  - 问题: 
    - 当 Docker Hub 返回重定向到 AWS S3/Cloudflare R2 时,代理缺少 AWS 签名头 (`Missing x-amz-content-sha256`)
    - Docker Hub CDN (`production.cloudflare.docker.com`) 在国内被墙,客户端无法直接访问
    - Cloudflare R2 存储需要 AWS 签名请求,代理跟随会破坏签名
  - 解决方案: 
    - **Docker Hub CDN** (`*.cloudflare.docker.com`, `*.docker.com`, `*.docker.io`): 代理服务器跟随重定向并代理下载
    - **外部存储** (AWS S3, Cloudflare R2, GCS, Azure Blob): 直接返回重定向给客户端
    - **其他重定向**: 代理服务器尝试跟随
  - 影响: 
    - ✅ 支持所有标准重定向状态码 (301, 302, 303, 307, 308)
    - ✅ 智能识别 Docker Hub CDN 并代理下载 (解决国内访问问题)
    - ✅ 自动检测外部存储域名 (amazonaws.com, cloudfront.net, cloudflarestorage.com, storage.googleapis.com, blob.core.windows.net)
    - ✅ 避免了复杂的 AWS Signature V4 签名处理
    - ✅ 支持 Cloudflare R2 存储 (docker-images-prod.*.r2.cloudflarestorage.com)
  - 测试: 新增 `test-aws-redirect.sh` 测试脚本

### 🧪 测试
- 新增 `test-aws-redirect.sh` - AWS S3 重定向测试脚本
  - Manifest 请求测试
  - Blob 重定向验证
  - 完整镜像拉取测试

## [v1.0.0] - 2024-12-XX

## 基于 ciiiii/cloudflare-docker-proxy 的改进

### 主要变更

#### 1. 路由规则调整 ✅
- **变更前**: 使用 `registry.docker.{domain}` 格式
- **变更后**: 使用 `docker.{domain}` 格式
- **原因**: 与 [ciiiii/cloudflare-docker-proxy](https://github.com/ciiiii/cloudflare-docker-proxy) 完全兼容

**影响的路由**:
```
旧格式                              → 新格式
registry.docker.{domain}           → docker.{domain}
quay.registry.docker.{domain}      → quay.{domain}
gcr.registry.docker.{domain}       → gcr.{domain}
k8s-gcr.registry.docker.{domain}   → k8s-gcr.{domain}
k8s.registry.docker.{domain}       → k8s.{domain}
ghcr.registry.docker.{domain}      → ghcr.{domain}
cloudsmith.registry.docker.{domain}→ cloudsmith.{domain}
ecr.registry.docker.{domain}       → ecr.{domain}
```

#### 2. /v2/ 端点认证处理 ✅
- **新增**: 在 `/v2/` 端点检查认证状态
- **行为**: 如果上游返回 401，则返回认证挑战
- **原因**: 符合 Docker Registry V2 API 规范和 ciiiii 实现

#### 3. 文档改进 ✅
- **新增**: `ARCHITECTURE.md` - 详细的架构设计文档
- **新增**: `.env.example` - 配置示例文件
- **新增**: `test.sh` - 功能测试脚本
- **更新**: `README.md` - 添加功能对比表和迁移指南

### 完整的功能对比

| 功能 | ciiiii/cloudflare-docker-proxy | go-docker-proxy | 状态 |
|------|-------------------------------|-----------------|------|
| 路由规则 | `docker.{domain}` | `docker.{domain}` | ✅ 完全一致 |
| Docker Hub | ✅ | ✅ | ✅ |
| Quay.io | ✅ | ✅ | ✅ |
| GCR | ✅ | ✅ | ✅ |
| GHCR | ✅ | ✅ | ✅ |
| K8s Registry | ✅ | ✅ | ✅ |
| 认证流程 | ✅ | ✅ | ✅ |
| Library 重定向 | ✅ | ✅ | ✅ |
| Blob 重定向 | ✅ | ✅ | ✅ |
| 文件缓存 | ❌ | ✅ | 🚀 新增 |
| 调试日志 | 有限 | ✅ 详细 | 🚀 增强 |

### 测试验证

运行测试脚本验证功能：

```bash
# 启动服务
export CUSTOM_DOMAIN=example.com
export DEBUG=true
go run . &

# 等待服务启动
sleep 2

# 运行测试
./test.sh

# 测试结果示例：
# ✅ 健康检查通过
# ✅ 重定向正常
# ✅ /v2/ 端点响应
# ✅ 路由查询成功
# ✅ 所有仓库路由正常
```

### 迁移指南

#### 从旧版本迁移

如果你之前使用的是 `registry.docker.{domain}` 格式：

1. **更新 DNS 记录**:
   ```
   旧: registry.docker.example.com → 服务器IP
   新: docker.example.com → 服务器IP
   ```

2. **更新 Docker 配置**:
   ```json
   {
     "registry-mirrors": [
       "https://docker.example.com"
     ]
   }
   ```

3. **无需修改代码**: 只需重新部署即可

#### 从 Cloudflare Worker 迁移

完全无缝迁移！

1. 保持相同的 `CUSTOM_DOMAIN`
2. DNS 记录指向你的服务器
3. 所有现有配置继续工作

### 技术实现细节

#### 路由匹配

```go
// buildRoutes 函数现在使用简洁格式
routes := map[string]string{
    fmt.Sprintf("docker.%s", customDomain): dockerHub,
    fmt.Sprintf("quay.%s", customDomain):   "https://quay.io",
    // ...
}
```

#### /v2/ 端点处理

```go
// 新增认证检查
func (p *ProxyServer) handleV2Root(w http.ResponseWriter, r *http.Request) {
    // ... 路由匹配 ...
    
    resp, err := p.transport.RoundTrip(req)
    if resp.StatusCode == http.StatusUnauthorized {
        p.responseUnauthorized(w, r)  // 返回认证挑战
        return
    }
    
    p.copyResponseRoundTrip(w, resp)
}
```

### 性能指标

基于初步测试（需要更详细的基准测试）：

- **首次请求**: ~500ms（包含上游请求）
- **缓存命中**: ~5ms（本地文件读取）
- **内存占用**: ~20MB（空闲）
- **缓存效率**: Manifest 和 Blob 100% 缓存

### 下一步计划

- [ ] 添加 Prometheus 监控指标
- [ ] 实现缓存预热功能
- [ ] 支持 Redis 作为缓存后端
- [ ] 添加速率限制功能
- [ ] 实现 WebUI 管理界面

### 相关资源

- [ciiiii/cloudflare-docker-proxy](https://github.com/ciiiii/cloudflare-docker-proxy) - 原始 Cloudflare Worker 实现
- [goproxy/goproxy](https://github.com/goproxy/goproxy) - 缓存系统参考
- [Docker Registry V2 API](https://docs.docker.com/registry/spec/api/) - API 规范
- [ARCHITECTURE.md](./ARCHITECTURE.md) - 详细架构文档

### 贡献

欢迎提交 Issue 和 Pull Request！

### 许可证

与原项目保持一致。
