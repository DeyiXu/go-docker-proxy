# 生产环境更新指南 - v1.1.0

## 更新内容

本次更新修复了 AWS S3 重定向处理的关键问题,解决了 Docker Hub 镜像拉取失败的错误。

### 关键修复
- ✅ 修复 `Missing x-amz-content-sha256` 错误
- ✅ 支持 Docker Hub → AWS S3/CloudFront 重定向
- ✅ 优化下载性能(客户端直接从 CDN 下载)

## 更新步骤

### 方法1: Docker 部署更新

```bash
# 1. 停止现有服务
docker-compose down

# 2. 拉取最新代码
git pull origin main

# 3. 重新构建镜像
docker-compose build

# 4. 启动服务
docker-compose up -d

# 5. 验证服务
docker-compose logs -f
```

### 方法2: 二进制部署更新

```bash
# 1. 备份当前运行的二进制文件
sudo cp /usr/local/bin/go-docker-proxy /usr/local/bin/go-docker-proxy.bak

# 2. 拉取最新代码
cd /path/to/go-docker-proxy
git pull origin main

# 3. 编译新版本
go build -o go-docker-proxy

# 4. 停止服务
sudo systemctl stop go-docker-proxy

# 5. 替换二进制文件
sudo cp go-docker-proxy /usr/local/bin/

# 6. 启动服务
sudo systemctl start go-docker-proxy

# 7. 验证服务
sudo systemctl status go-docker-proxy
sudo journalctl -u go-docker-proxy -f
```

### 方法3: 使用部署脚本 (推荐)

```bash
# 1. 拉取最新代码
git pull origin main

# 2. 运行部署脚本
./deploy.sh

# 脚本会自动:
# - 检测现有安装
# - 停止旧服务
# - 编译新版本
# - 启动新服务
# - 验证运行状态
```

## 测试验证

### 1. 快速测试
```bash
# 运行自动化测试
./test-aws-redirect.sh registry.w4w.cc:8080
```

### 2. 手动验证

```bash
# 测试 Manifest
curl -I http://registry.w4w.cc:8080/v2/nginx/manifests/latest

# 预期: HTTP/1.1 200 OK

# 测试 Blob 重定向
curl -I http://registry.w4w.cc:8080/v2/nginx/blobs/sha256:xxx

# 预期: HTTP/1.1 301/307 (重定向到 amazonaws.com 或 cloudfront.net)

# 完整拉取测试
docker pull registry.w4w.cc:8080/nginx:latest

# 预期: 成功下载所有层
```

### 3. 监控检查

```bash
# 使用监控脚本
./monitor.sh

# 选择:
# 1) 实时监控 - 查看请求日志
# 2) 性能测试 - 验证拉取速度
```

## 回滚计划

如果更新后遇到问题,可以快速回滚:

### Docker 部署回滚
```bash
# 1. 停止新版本
docker-compose down

# 2. 回退代码
git reset --hard HEAD~1

# 3. 重新构建旧版本
docker-compose build

# 4. 启动旧版本
docker-compose up -d
```

### 二进制部署回滚
```bash
# 1. 停止服务
sudo systemctl stop go-docker-proxy

# 2. 恢复备份
sudo cp /usr/local/bin/go-docker-proxy.bak /usr/local/bin/go-docker-proxy

# 3. 启动服务
sudo systemctl start go-docker-proxy
```

## 预期效果

### 修复前
```
❌ docker pull registry.w4w.cc:8080/nginx:latest
Error: Missing x-amz-content-sha256
```

### 修复后
```
✅ docker pull registry.w4w.cc:8080/nginx:latest
latest: Pulling from nginx
a2abf6c4d29d: Pull complete
...
Status: Downloaded newer image for registry.w4w.cc:8080/nginx:latest
```

## 性能对比

| 场景 | 修复前 | 修复后 |
|-----|--------|--------|
| Manifest 请求 | ✅ 200 OK | ✅ 200 OK |
| Blob 下载 | ❌ 400 Error | ✅ 301 → 直接从 S3 |
| 完整拉取 | ❌ 失败 | ✅ 成功 |
| 下载速度 | N/A | 提升(直接 CDN) |
| 代理负载 | N/A | 降低(不转发 blob) |

## 注意事项

1. **无需配置变更** - 此更新不需要修改配置文件
2. **缓存保留** - 更新不会影响现有缓存数据
3. **向后兼容** - 完全兼容旧版本的所有功能
4. **零停机更新** - 可以使用蓝绿部署实现零停机

## 蓝绿部署(零停机)

```bash
# 1. 启动新版本在不同端口
PORT=8081 docker-compose up -d

# 2. 验证新版本正常工作
./test-aws-redirect.sh localhost:8081

# 3. 更新 Nginx upstream 配置
# 将流量切换到新端口

# 4. 停止旧版本
docker-compose -f docker-compose.old.yml down
```

## 技术支持

如有问题,请查阅:
1. [AWS_REDIRECT_FIX.md](./AWS_REDIRECT_FIX.md) - 详细技术说明
2. [ARCHITECTURE.md](./ARCHITECTURE.md) - 架构文档
3. [GitHub Issues](https://github.com/DeyiXu/go-docker-proxy/issues)

## 相关资源

- 📦 Docker Hub: https://hub.docker.com/
- 🔧 测试脚本: `test-aws-redirect.sh`
- 📊 监控脚本: `monitor.sh`
- 📝 完整更新日志: [CHANGELOG.md](./CHANGELOG.md)
