# Go Docker Proxy

基于Go语言实现的Docker镜像代理服务，完全兼容 `cloudflare-docker-proxy` 的路由规则和功能。

## 特性

- 🚀 完全兼容原版 `cloudflare-docker-proxy` 的路由配置
- 🎯 支持多个Docker镜像仓库代理（Docker Hub、Quay、GCR、GHCR等）
- 💾 实现文件缓存，提升访问速度  
- 🔐 完整的Docker Hub认证处理
- 🔄 支持Docker Hub library镜像自动重定向
- ⚡ 使用 `transport.RoundTrip` 提供最佳性能
- 🐳 轻量级，易于部署

## 快速开始

### 使用Docker运行

```bash
# 克隆仓库
git clone https://github.com/DeyiXu/go-docker-proxy.git
cd go-docker-proxy

# 设置自定义域名
export CUSTOM_DOMAIN=your-domain.com

# 启动服务
docker-compose up -d
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

服务会根据 `CUSTOM_DOMAIN` 自动生成以下路由规则：

#### 生产环境路由
- `registry.docker.{CUSTOM_DOMAIN}` → Docker Hub
- `quay.registry.docker.{CUSTOM_DOMAIN}` → Quay.io
- `gcr.registry.docker.{CUSTOM_DOMAIN}` → Google Container Registry
- `k8s-gcr.registry.docker.{CUSTOM_DOMAIN}` → Kubernetes GCR
- `k8s.registry.docker.{CUSTOM_DOMAIN}` → Kubernetes Registry
- `ghcr.registry.docker.{CUSTOM_DOMAIN}` → GitHub Container Registry
- `cloudsmith.registry.docker.{CUSTOM_DOMAIN}` → Cloudsmith Docker
- `ecr.registry.docker.{CUSTOM_DOMAIN}` → AWS ECR Public

#### 简化路由（仅生产环境）
- `docker.{CUSTOM_DOMAIN}` → Docker Hub
- `hub.{CUSTOM_DOMAIN}` → Docker Hub
- `registry.{CUSTOM_DOMAIN}` → Docker Hub

#### 本地开发路由
当 `CUSTOM_DOMAIN=localhost` 时：
- `docker.localhost` → Docker Hub
- `hub.localhost` → Docker Hub
- `registry.localhost` → Docker Hub

## 使用方法

### 配置Docker客户端

```bash
# 方法1: 修改 /etc/docker/daemon.json
{
  "registry-mirrors": [
    "https://registry.docker.your-domain.com"
  ]
}

# 方法2: 直接使用完整镜像名
docker pull registry.docker.your-domain.com/library/nginx:latest
docker pull quay.registry.docker.your-domain.com/prometheus/prometheus:latest
docker pull gcr.registry.docker.your-domain.com/google-containers/pause:latest
```

### DNS 配置

如需使用自定义域名，请配置 DNS：

```dns
# A 记录
registry.docker.your-domain.com     A     YOUR_SERVER_IP
quay.registry.docker.your-domain.com   A     YOUR_SERVER_IP
gcr.registry.docker.your-domain.com    A     YOUR_SERVER_IP
# ... 其他子域名
```

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