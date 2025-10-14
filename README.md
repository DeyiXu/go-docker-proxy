# Go Docker Proxy

基于Go语言实现的Docker镜像代理服务，完全兼容 [ciiiii/cloudflare-docker-proxy](https://github.com/ciiiii/cloudflare-docker-proxy) 的路由规则和功能。

## 特性

- 🚀 **完全兼容** [ciiiii/cloudflare-docker-proxy](https://github.com/ciiiii/cloudflare-docker-proxy) 的路由配置
- 🎯 支持多个Docker镜像仓库代理（Docker Hub、Quay、GCR、GHCR等）
- 💾 独立设计的两层缓存系统(内存索引+磁盘存储)，专为 Docker Registry 优化
- 🔐 完整的Docker Registry V2认证流程
- 🔄 自动处理Docker Hub library镜像重定向
- ⚡ 使用 `http.Transport.RoundTrip` 提供最佳性能
- 🌏 **针对跨区域部署优化**，支持全球高速访问
- 📝 详细的调试日志支持
- 🐳 轻量级，易于部署和维护
- 📚 完整的文档体系

## 📖 文档导航

### 快速开始
- **[快速部署指南](./QUICK_START.md)** - 10分钟完成快速部署 🚀
- **[快速参考卡片](./REFERENCE_CARD.md)** - 常用命令和配置速查

### 部署文档
- **[完整部署指南](./DEPLOYMENT_GUIDE.md)** - 详细部署指南(全球访问优化) ⭐
- **[网络优化配置](./NETWORK_OPTIMIZATION.md)** - 系统和应用层网络优化详解

### 技术文档
- **[架构文档](./ARCHITECTURE.md)** - 系统架构和设计原理
- **[优化方案总结](./OPTIMIZATION_SUMMARY.md)** - 性能优化总结
- **[变更日志](./CHANGELOG.md)** - 版本更新记录

### 合规性
- **[合规性说明](./COMPLIANCE.md)** - 文档合规性和技术中立原则

### 实用工具
- **`deploy.sh`** - 一键部署脚本(自动安装和配置)
- **`monitor.sh`** - 服务监控脚本(实时状态、日志、性能测试)

## 快速开始

### 使用Docker运行

```bash
# 克隆仓库
git clone https://github.com/DeyiXu/go-docker-proxy.git
cd go-docker-proxy

# 创建持久化缓存目录
mkdir -p cache

# 设置自定义域名
export CUSTOM_DOMAIN=your-domain.com

# 启动服务
docker compose up -d
```

### 本地开发

```bash
# 设置环境变量
export CUSTOM_DOMAIN=localhost
export DEBUG=true
export PORT=8080

# 运行服务
go mod tidy
go run .
```

## 配置

### 环境变量

- `CUSTOM_DOMAIN`: 自定义域名 (默认: example.com)
- `PORT`: 服务端口 (默认: 8080)
- `CACHE_DIR`: 缓存目录 (默认: ./cache)
- `DEBUG`: 调试模式 (默认: false)
- `TARGET_UPSTREAM`: 调试模式下的默认上游 (可选)

### 路由配置

服务会根据 `CUSTOM_DOMAIN` 自动生成以下路由规则（与 ciiiii/cloudflare-docker-proxy 完全兼容）：

#### 生产环境路由
- `docker.{CUSTOM_DOMAIN}` → Docker Hub
- `quay.{CUSTOM_DOMAIN}` → Quay.io
- `gcr.{CUSTOM_DOMAIN}` → Google Container Registry
- `k8s-gcr.{CUSTOM_DOMAIN}` → Kubernetes GCR
- `k8s.{CUSTOM_DOMAIN}` → Kubernetes Registry
- `ghcr.{CUSTOM_DOMAIN}` → GitHub Container Registry
- `cloudsmith.{CUSTOM_DOMAIN}` → Cloudsmith Docker
- `ecr.{CUSTOM_DOMAIN}` → AWS ECR Public

#### 过渡路由
- `docker-staging.{CUSTOM_DOMAIN}` → Docker Hub (staging)

## 使用方法

### 配置Docker客户端

```bash
# 方法1: 修改 /etc/docker/daemon.json
{
  "registry-mirrors": [
    "https://docker.your-domain.com"
  ]
}

# 方法2: 直接使用完整镜像名
docker pull docker.your-domain.com/library/nginx:latest
docker pull quay.your-domain.com/prometheus/prometheus:latest
docker pull gcr.your-domain.com/google-containers/pause:latest
docker pull ghcr.your-domain.com/owner/repo:latest
docker pull k8s.your-domain.com/kube-apiserver:latest
```

### DNS 配置

如需使用自定义域名，请配置 DNS：

```dns
# A 记录（与 ciiiii/cloudflare-docker-proxy 完全兼容）
docker.your-domain.com       A     YOUR_SERVER_IP
quay.your-domain.com         A     YOUR_SERVER_IP
gcr.your-domain.com          A     YOUR_SERVER_IP
k8s.your-domain.com          A     YOUR_SERVER_IP
ghcr.your-domain.com         A     YOUR_SERVER_IP
# ... 其他子域名
```

## 与 Cloudflare Worker 版本的对比

| 功能 | ciiiii/cloudflare-docker-proxy | go-docker-proxy |
|------|-------------------------------|-----------------|
| 路由规则 | ✅ 完全一致 | ✅ 完全一致 |
| 多仓库支持 | ✅ | ✅ |
| Docker Hub 认证 | ✅ | ✅ |
| Library 镜像重定向 | ✅ | ✅ |
| 文件缓存 | ❌ (Workers KV) | ✅ 磁盘缓存 |
| 自托管部署 | ❌ | ✅ |
| 调试日志 | 有限 | ✅ 详细日志 |
| 运行环境 | Cloudflare Workers | 任意服务器 |

### 从 Cloudflare Worker 迁移

无需修改任何配置！只需：

1. 使用相同的 `CUSTOM_DOMAIN` 环境变量
2. DNS 记录指向你的服务器
3. 所有路由完全兼容，无需修改 Docker 配置

## API接口

- `GET /`: 重定向到 `/v2/` 或返回路由信息
- `GET /v2/`: Docker Registry v2 API根路径
- `GET /v2/auth`: 认证接口
- `GET /v2/*`: 其他Docker Registry API请求

### 路由查询

访问未配置的域名时，会返回可用路由列表：

```bash
curl http://unknown-domain.com:8080/
```

```json
{
  "routes": {
    "registry.docker.your-domain.com": "https://registry-1.docker.io",
    "quay.registry.docker.your-domain.com": "https://quay.io",
    ...
  },
  "message": "Available registry routes"
}
```

## 性能优化

### 缓存机制
- 支持内存缓存和文件缓存双重机制
- 自动缓存 manifest 和 blob 数据
- 异步缓存处理，不阻塞请求
- 支持缓存过期和自动清理

### 网络优化
- 使用 `http.Transport.RoundTrip` 底层API
- 连接池复用和长连接保持
- HTTP/2 支持
- 智能重定向处理

## 部署建议

### 跨区域部署 - 全球访问优化 🌏

**如果您需要部署服务,同时保证全球用户能够正常访问,请查看详细的部署指南:**

👉 **[完整部署指南](./DEPLOYMENT_GUIDE.md)** - 包含:
- 地理位置选择建议(亚太/欧洲/美洲等区域)
- 网络层优化(BBR、连接池、HTTP/2)
- CDN加速配置(Cloudflare 免费方案)
- Nginx 反向代理配置
- SSL证书自动化
- 性能监控和故障排查

#### 一键部署脚本

```bash
# 下载项目
git clone https://github.com/DeyiXu/go-docker-proxy.git
cd go-docker-proxy

# 上传到服务器后,运行一键部署脚本
sudo ./deploy.sh
```

脚本会自动完成:
- ✅ 安装 Docker 和依赖
- ✅ 优化网络参数(BBR拥塞控制)
- ✅ 配置防火墙规则
- ✅ 部署应用容器
- ✅ 可选安装 Nginx + SSL

#### 推荐部署架构

```
全球用户
    ↓
Cloudflare CDN (免费)
    ↓
服务器(选择网络延迟低的区域)
    ↓
Nginx (反向代理 + SSL)
    ↓
go-docker-proxy (Docker容器)
    ↓
Docker Hub / Quay / GCR 等上游仓库
```

#### 性能参考

| 部署地区 | 典型延迟 | 下载速度 | 推荐度 |
|---------|---------|---------|--------|
| 亚太区域  | 20-50ms  | 10-50MB/s | ⭐⭐⭐⭐⭐ |
| 亚洲其他  | 60-100ms | 5-30MB/s  | ⭐⭐⭐⭐ |
| 全球其他  | 80-150ms | 5-20MB/s  | ⭐⭐⭐ |

### 生产环境
1. 使用 HTTPS 证书
2. 配置适当的缓存大小
3. 设置日志轮转
4. 监控服务健康状态

### Kubernetes 部署

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: go-docker-proxy
spec:
  replicas: 3
  selector:
    matchLabels:
      app: go-docker-proxy
  template:
    metadata:
      labels:
        app: go-docker-proxy
    spec:
      containers:
      - name: go-docker-proxy
        image: go-docker-proxy:latest
        ports:
        - containerPort: 8080
        env:
        - name: CUSTOM_DOMAIN
          value: "your-domain.com"
        - name: CACHE_DIR
          value: "/app/cache"
        volumeMounts:
        - name: cache
          mountPath: /app/cache
      volumes:
      - name: cache
        emptyDir: {}
```

## 与原版对比

| 特性 | cloudflare-docker-proxy | go-docker-proxy |
|------|-------------------------|-----------------|
| 运行环境 | Cloudflare Workers | 独立服务器 |
| 语言 | JavaScript | Go |
| 缓存 | Cloudflare 边缘缓存 | 本地文件缓存 |
| 性能 | 边缘计算优势 | 本地处理优势 |
| 部署 | 依赖Cloudflare | 可部署任意环境 |
| 功能兼容性 | 100% | 100% |

## 故障排除

### 常见问题

1. **认证失败**
   - 检查域名配置是否正确
   - 确认上游仓库可访问

2. **缓存问题**
   - 检查缓存目录权限
   - 清理过期缓存文件

3. **网络连接**
   - 检查防火墙设置
   - 验证DNS解析

### 调试模式

```bash
DEBUG=true go run .
```

调试模式会输出详细的请求日志和路由信息。