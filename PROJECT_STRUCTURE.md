# 项目文件结构

```
go-docker-proxy/
├── 📝 核心代码
│   ├── main.go                    # 主程序入口和代理服务器实现
│   ├── cache.go                   # 两层缓存系统实现
│   ├── go.mod                     # Go模块依赖
│   └── go.sum                     # 依赖校验和
│
├── 🚀 部署配置
│   ├── Dockerfile                 # Docker镜像构建文件
│   ├── docker-compose.yml         # Docker Compose配置
│   └── .env.example               # 环境变量示例
│
├── 🛠️ 部署脚本
│   ├── deploy-overseas.sh         # 一键部署脚本(境外服务器)
│   ├── monitor.sh                 # 服务监控脚本
│   └── test.sh                    # 功能测试脚本
│
├── 📚 文档系统
│   ├── README.md                  # 项目主文档
│   ├── QUICKSTART.md              # 快速开始指南(10分钟部署)
│   ├── DEPLOYMENT_CN.md           # 境外部署完整指南
│   ├── NETWORK_OPTIMIZATION.md    # 网络优化配置详解
│   ├── OPTIMIZATION_SUMMARY.md    # 优化总结
│   ├── ARCHITECTURE.md            # 系统架构文档
│   └── CHANGELOG.md               # 版本变更日志
│
├── 🗂️ 其他文件
│   ├── LICENSE                    # 开源协议
│   ├── cache/                     # 缓存目录(运行时创建)
│   │   ├── manifests/            # Manifest缓存
│   │   └── blobs/                # Blob缓存
│   └── cache.go.bak              # 原缓存实现备份
```

## 文档说明

### 📖 核心文档(必读)

#### 1. [README.md](./README.md)
- **内容**: 项目概述、特性、快速开始
- **适合**: 所有用户
- **阅读时间**: 5分钟

#### 2. [QUICKSTART.md](./QUICKSTART.md) ⭐
- **内容**: 10分钟快速部署指南
- **适合**: 想要快速部署的用户
- **阅读时间**: 10分钟
- **包含**: 
  - 一键自动部署
  - 手动部署步骤
  - DNS配置
  - SSL证书配置
  - 常见问题

### 🚀 部署文档(推荐)

#### 3. [DEPLOYMENT_CN.md](./DEPLOYMENT_CN.md) ⭐⭐⭐
- **内容**: 境外部署完整指南(中国大陆访问优化)
- **适合**: 需要详细了解部署细节的用户
- **阅读时间**: 30分钟
- **包含**:
  - 地理位置选择(香港/新加坡/东京)
  - DNS配置(智能解析)
  - CDN配置(Cloudflare)
  - Nginx反向代理
  - SSL证书管理
  - 监控和告警
  - 成本估算
  - 性能基准

#### 4. [NETWORK_OPTIMIZATION.md](./NETWORK_OPTIMIZATION.md)
- **内容**: 深度网络优化配置
- **适合**: 需要极致性能的用户
- **阅读时间**: 45分钟
- **包含**:
  - 系统级TCP优化
  - BBR拥塞控制
  - Nginx完整配置
  - Cloudflare高级配置
  - 性能测试方法
  - 故障排查

### 📊 技术文档

#### 5. [ARCHITECTURE.md](./ARCHITECTURE.md)
- **内容**: 系统架构和设计原理
- **适合**: 开发者、技术人员
- **阅读时间**: 20分钟
- **包含**:
  - 路由系统设计
  - 认证流程
  - 缓存系统架构
  - 性能优化策略

#### 6. [OPTIMIZATION_SUMMARY.md](./OPTIMIZATION_SUMMARY.md)
- **内容**: 所有优化措施的总结
- **适合**: 想了解整体优化思路的用户
- **阅读时间**: 15分钟
- **包含**:
  - 优化清单
  - 性能对比
  - 成本分析
  - 最佳实践

#### 7. [CHANGELOG.md](./CHANGELOG.md)
- **内容**: 版本更新记录
- **适合**: 想了解版本历史的用户
- **阅读时间**: 5分钟

## 脚本说明

### 🛠️ 部署脚本

#### 1. deploy-overseas.sh ⭐⭐⭐
```bash
sudo ./deploy-overseas.sh
```

**功能**:
- ✅ 自动检测操作系统
- ✅ 安装Docker和依赖
- ✅ 优化网络参数(BBR)
- ✅ 配置防火墙
- ✅ 部署应用容器
- ✅ 配置Nginx(可选)
- ✅ 配置SSL证书(可选)

**适用场景**: 首次部署到境外服务器

**执行时间**: 5-10分钟

#### 2. monitor.sh ⭐⭐
```bash
# 持续监控
./monitor.sh -m

# 单次检查
./monitor.sh -c

# 查看日志
./monitor.sh -l 100

# 性能测试
./monitor.sh -p

# 清理缓存
./monitor.sh -C
```

**功能**:
- ✅ 实时监控(CPU、内存、网络)
- ✅ 健康检查
- ✅ 日志查看
- ✅ 性能测试
- ✅ 缓存管理
- ✅ 统计信息

**适用场景**: 服务运维和监控

#### 3. test.sh
```bash
./test.sh
```

**功能**:
- ✅ Docker Registry API测试
- ✅ 认证流程测试
- ✅ Manifest下载测试
- ✅ Blob下载测试
- ✅ 缓存功能测试

**适用场景**: 部署后功能验证

## 阅读路径推荐

### 🎯 路径1: 快速部署(新手)

1. [README.md](./README.md) - 了解项目概况
2. [QUICKSTART.md](./QUICKSTART.md) - 跟随步骤部署
3. 使用 `deploy-overseas.sh` 一键部署
4. 使用 `monitor.sh` 监控服务

**预计时间**: 30分钟

### 🎯 路径2: 深入理解(进阶)

1. [README.md](./README.md) - 项目概况
2. [ARCHITECTURE.md](./ARCHITECTURE.md) - 理解架构
3. [DEPLOYMENT_CN.md](./DEPLOYMENT_CN.md) - 详细部署
4. [NETWORK_OPTIMIZATION.md](./NETWORK_OPTIMIZATION.md) - 性能优化
5. [OPTIMIZATION_SUMMARY.md](./OPTIMIZATION_SUMMARY.md) - 优化总结

**预计时间**: 2-3小时

### 🎯 路径3: 运维维护(运维人员)

1. [QUICKSTART.md](./QUICKSTART.md) - 快速部署
2. [DEPLOYMENT_CN.md](./DEPLOYMENT_CN.md) - 监控和告警
3. 熟练使用 `monitor.sh` 监控脚本
4. [NETWORK_OPTIMIZATION.md](./NETWORK_OPTIMIZATION.md) - 故障排查

**预计时间**: 1小时

### 🎯 路径4: 开发调试(开发者)

1. [ARCHITECTURE.md](./ARCHITECTURE.md) - 系统架构
2. 阅读 `main.go` 和 `cache.go` 源码
3. [NETWORK_OPTIMIZATION.md](./NETWORK_OPTIMIZATION.md) - 性能优化
4. 使用 `test.sh` 测试脚本

**预计时间**: 2-3小时

## 核心功能文件映射

### 路由系统
- **文件**: `main.go` → `buildRoutes()`
- **文档**: [ARCHITECTURE.md](./ARCHITECTURE.md) - "路由系统"章节
- **配置**: 环境变量 `CUSTOM_DOMAIN`

### 认证流程
- **文件**: `main.go` → `handleV2Root()`, `handleAuth()`
- **文档**: [ARCHITECTURE.md](./ARCHITECTURE.md) - "认证流程"章节
- **规范**: Docker Registry V2 API

### 缓存系统
- **文件**: `cache.go` → `DockerRegistryCache`
- **文档**: 
  - [ARCHITECTURE.md](./ARCHITECTURE.md) - "缓存系统"章节
  - [OPTIMIZATION_SUMMARY.md](./OPTIMIZATION_SUMMARY.md) - "智能两层缓存"章节
- **配置**: 环境变量 `CACHE_DIR`

### 代理转发
- **文件**: `main.go` → `proxyRequest()`
- **文档**: [ARCHITECTURE.md](./ARCHITECTURE.md) - "代理转发"章节
- **优化**: HTTP/2, Keep-Alive, 连接池

## 配置文件说明

### docker-compose.yml
```yaml
# 生产环境配置
services:
  go-docker-proxy:
    image: ghcr.io/deyixu/go-docker-proxy:latest
    ports:
      - "8080:8080"
    environment:
      - PORT=8080                    # 服务端口
      - CACHE_DIR=/cache             # 缓存目录
      - DEBUG=false                  # 调试模式
      - CUSTOM_DOMAIN=yourdomain.com # 自定义域名
    volumes:
      - ./cache:/cache               # 缓存持久化
    restart: unless-stopped          # 自动重启
```

### Dockerfile
- **基础镜像**: golang:1.23-alpine
- **最终镜像**: scratch(最小化)
- **大小**: ~15MB
- **特性**: 静态编译、多阶段构建

## 依赖说明

### Go 依赖
```go
require (
    github.com/go-chi/chi/v5 v5.0.0  // 路由框架
)
```

**无其他外部依赖** - 全部使用标准库实现:
- `net/http` - HTTP服务器和客户端
- `crypto/sha256` - 哈希计算
- `encoding/json` - JSON处理
- `sync` - 并发控制
- `time` - 时间处理

### 系统依赖
- Docker 20.10+
- Linux Kernel 4.9+ (BBR支持)
- Nginx 1.18+ (可选)
- Certbot (可选,用于SSL)

## 运行时目录

### 缓存目录结构
```
cache/
├── manifests/              # Manifest缓存
│   ├── 12/
│   │   ├── 34/
│   │   │   └── 1234567890abcdef...json
│   │   │   └── 1234567890abcdef...meta
│   └── ...
└── blobs/                  # Blob缓存
    ├── ab/
    │   ├── cd/
    │   │   └── abcdef1234567890...blob
    │   │   └── abcdef1234567890...meta
    └── ...
```

### 日志位置
- **应用日志**: `docker logs go-docker-proxy`
- **Nginx日志**: `/var/log/nginx/access.log`, `/var/log/nginx/error.log`
- **系统日志**: `journalctl -u docker`

## 端口说明

| 端口 | 用途 | 访问方式 |
|-----|------|---------|
| 8080 | 应用端口 | `http://localhost:8080` |
| 80 | HTTP(Nginx) | `http://yourdomain.com` |
| 443 | HTTPS(Nginx) | `https://yourdomain.com` |

## 环境变量

| 变量名 | 默认值 | 说明 |
|-------|--------|------|
| `PORT` | 8080 | 服务监听端口 |
| `CACHE_DIR` | ./cache | 缓存目录 |
| `DEBUG` | false | 调试模式 |
| `CUSTOM_DOMAIN` | example.com | 自定义域名 |
| `TARGET_UPSTREAM` | (空) | 调试用上游地址 |

## 常用命令

### 部署相关
```bash
# 一键部署
sudo ./deploy-overseas.sh

# 手动部署
docker compose up -d

# 查看状态
docker compose ps

# 查看日志
docker compose logs -f

# 重启服务
docker compose restart

# 停止服务
docker compose down
```

### 监控相关
```bash
# 持续监控
./monitor.sh -m

# 健康检查
./monitor.sh -c

# 性能测试
./monitor.sh -p

# 查看日志
./monitor.sh -l 100
```

### 维护相关
```bash
# 清理缓存
rm -rf cache/*

# 查看缓存大小
du -sh cache/

# 更新服务
docker compose pull
docker compose up -d

# 备份配置
tar czf backup.tar.gz docker-compose.yml cache/
```

## 故障排查文件

| 问题类型 | 查看文件/命令 |
|---------|-------------|
| 服务无法启动 | `docker logs go-docker-proxy` |
| 网络连接问题 | `docker compose logs` |
| 缓存问题 | `ls -lah cache/` |
| Nginx问题 | `/var/log/nginx/error.log` |
| SSL证书问题 | `certbot certificates` |
| 系统资源 | `./monitor.sh -s` |

## 更新历史

查看 [CHANGELOG.md](./CHANGELOG.md) 了解版本更新历史。

## 贡献指南

欢迎贡献代码和文档改进!

1. Fork 项目
2. 创建特性分支
3. 提交更改
4. 推送到分支
5. 创建 Pull Request

## 许可证

查看 [LICENSE](./LICENSE) 文件。

## 联系方式

- **GitHub**: https://github.com/DeyiXu/go-docker-proxy
- **Issues**: https://github.com/DeyiXu/go-docker-proxy/issues
- **文档**: https://github.com/DeyiXu/go-docker-proxy/tree/main/docs

---

**快速开始**: 查看 [QUICKSTART.md](./QUICKSTART.md)
