# 生产环境部署验证清单 v1.1.0

## 更新前准备

### 1. 环境检查
- [ ] 确认当前版本号
- [ ] 检查服务运行状态
- [ ] 备份配置文件
- [ ] 记录当前性能基线

```bash
# 检查版本
curl http://registry.w4w.cc:8080/

# 检查服务状态
docker ps | grep go-docker-proxy
# 或
sudo systemctl status go-docker-proxy

# 性能基线测试
./monitor.sh  # 选择 "2) 性能测试"
```

### 2. 备份计划
- [ ] 备份二进制文件
- [ ] 导出 Docker 镜像
- [ ] 备份缓存数据(可选)
- [ ] 记录回滚步骤

```bash
# 备份二进制
sudo cp /usr/local/bin/go-docker-proxy /usr/local/bin/go-docker-proxy.v1.0.bak

# 导出 Docker 镜像
docker save go-docker-proxy:latest > go-docker-proxy-v1.0.tar

# 创建代码标签
git tag v1.0.0-backup
```

---

## 更新部署

### 3. 代码更新
- [ ] 拉取最新代码
- [ ] 查看变更内容
- [ ] 确认无配置文件冲突

```bash
cd /home/xdy/projects/DeyiXu/go-docker-proxy
git fetch origin main
git diff main origin/main  # 查看变更
git pull origin main
```

### 4. 构建新版本
- [ ] 编译通过无错误
- [ ] 二进制文件大小合理
- [ ] 依赖完整

```bash
# 编译
go build -o go-docker-proxy

# 检查二进制
ls -lh go-docker-proxy
file go-docker-proxy
```

### 5. 部署执行
- [ ] 停止旧服务
- [ ] 部署新版本
- [ ] 启动新服务
- [ ] 检查启动日志

```bash
# Docker 部署
docker-compose down
docker-compose build
docker-compose up -d
docker-compose logs -f

# 或系统服务部署
sudo systemctl stop go-docker-proxy
sudo cp go-docker-proxy /usr/local/bin/
sudo systemctl start go-docker-proxy
sudo journalctl -u go-docker-proxy -f
```

---

## 功能验证

### 6. 基础功能测试
- [ ] 健康检查端点正常
- [ ] 根路径返回服务信息
- [ ] 路由映射正确

```bash
# 健康检查
curl http://registry.w4w.cc:8080/health
# 预期: {"status":"ok"}

# 根路径
curl http://registry.w4w.cc:8080/
# 预期: Docker Proxy Server 信息

# 路由映射
curl http://registry.w4w.cc:8080/routes
```

### 7. 认证流程测试
- [ ] /v2/ 端点返回 401
- [ ] /v2/auth 返回 token
- [ ] Token 可用于后续请求

```bash
# 测试 /v2/
curl -I http://registry.w4w.cc:8080/v2/
# 预期: 401 Unauthorized + WWW-Authenticate header

# 获取 token
curl "http://registry.w4w.cc:8080/v2/auth?service=registry.docker.io&scope=repository:nginx:pull"
# 预期: {"token":"..."}
```

### 8. Manifest 请求测试
- [ ] 官方镜像 manifest 正常
- [ ] Library 重定向正常
- [ ] 缓存命中率正常

```bash
# 测试 nginx (library 镜像)
curl -H "Accept: application/vnd.docker.distribution.manifest.v2+json" \
  http://registry.w4w.cc:8080/v2/nginx/manifests/latest
# 预期: 200 OK + manifest JSON

# 检查重定向
curl -I http://registry.w4w.cc:8080/v2/nginx/manifests/latest
# 预期: 301 → /v2/library/nginx/manifests/latest

# 检查缓存
curl -I http://registry.w4w.cc:8080/v2/library/nginx/manifests/latest
# 第二次请求应该: X-Cache: HIT
```

### 9. Blob 重定向测试 ⭐ (重点)
- [ ] Blob 请求返回重定向
- [ ] 重定向到外部存储 (AWS S3/CloudFront)
- [ ] 客户端可以跟随重定向下载

```bash
# 获取一个 blob SHA
BLOB_SHA=$(curl -s http://registry.w4w.cc:8080/v2/library/nginx/manifests/latest | \
  jq -r '.layers[0].digest')

# 测试 blob 重定向
curl -I "http://registry.w4w.cc:8080/v2/library/nginx/blobs/$BLOB_SHA"
# 预期: 301/307 + Location: https://production.cloudflare.docker.com/...

# 验证重定向 URL 可访问
REDIRECT_URL=$(curl -sI "http://registry.w4w.cc:8080/v2/library/nginx/blobs/$BLOB_SHA" | \
  grep -i "Location:" | cut -d' ' -f2 | tr -d '\r')
curl -I "$REDIRECT_URL"
# 预期: 200 OK
```

### 10. 完整镜像拉取测试 ⭐⭐ (最重要)
- [ ] 小型镜像拉取成功 (alpine)
- [ ] 中型镜像拉取成功 (nginx)
- [ ] 大型镜像拉取成功 (ubuntu)

```bash
# 清理旧镜像
docker rmi registry.w4w.cc:8080/alpine:latest 2>/dev/null || true
docker rmi registry.w4w.cc:8080/nginx:latest 2>/dev/null || true

# 测试 alpine (小)
time docker pull registry.w4w.cc:8080/alpine:latest
# 预期: 成功,耗时 < 30s

# 测试 nginx (中)
time docker pull registry.w4w.cc:8080/nginx:latest
# 预期: 成功,耗时 < 60s

# 测试 ubuntu (大)
time docker pull registry.w4w.cc:8080/ubuntu:latest
# 预期: 成功,耗时 < 120s
```

### 11. 自动化测试脚本
- [ ] test-aws-redirect.sh 全部通过

```bash
./test-aws-redirect.sh registry.w4w.cc:8080
# 预期: 所有测试通过 ✓✓✓
```

---

## 性能验证

### 12. 缓存性能
- [ ] 缓存命中率 > 50%
- [ ] 缓存大小合理增长
- [ ] 过期清理正常

```bash
# 检查缓存统计
curl http://registry.w4w.cc:8080/stats

# 检查缓存目录
du -sh cache/
ls -lh cache/manifests/ cache/blobs/

# 触发缓存清理(等待30分钟或重启服务)
```

### 13. 响应时间
- [ ] Manifest 响应 < 500ms
- [ ] Blob 重定向响应 < 200ms
- [ ] 完整拉取时间合理

```bash
# 测试响应时间
time curl -s -o /dev/null http://registry.w4w.cc:8080/v2/library/nginx/manifests/latest
time curl -s -o /dev/null http://registry.w4w.cc:8080/v2/library/nginx/blobs/$BLOB_SHA

# 使用监控脚本
./monitor.sh  # 选择 "2) 性能测试"
```

### 14. 并发压测
- [ ] 支持 10+ 并发请求
- [ ] 无明显错误率
- [ ] 内存/CPU 使用正常

```bash
# 并发拉取测试
for i in {1..10}; do
  (docker pull registry.w4w.cc:8080/alpine:latest &)
done
wait

# 检查资源使用
docker stats go-docker-proxy
# 或
top -p $(pidof go-docker-proxy)
```

---

## 监控验证

### 15. 日志检查
- [ ] 无 ERROR 级别日志
- [ ] 请求日志正常
- [ ] 重定向日志清晰

```bash
# 检查错误日志
docker-compose logs --since 10m | grep ERROR
# 或
sudo journalctl -u go-docker-proxy --since "10 min ago" | grep ERROR

# 查看请求日志
docker-compose logs -f --tail 50
# 或
sudo journalctl -u go-docker-proxy -f
```

### 16. 指标监控
- [ ] CPU 使用率 < 50%
- [ ] 内存使用稳定
- [ ] 网络带宽正常
- [ ] 磁盘 I/O 正常

```bash
# 系统资源
htop
iostat -x 1 5

# Docker 统计
docker stats go-docker-proxy

# 实时监控
./monitor.sh  # 选择 "1) 实时监控"
```

---

## 回滚准备

### 17. 回滚计划确认
- [ ] 回滚步骤文档化
- [ ] 备份可用
- [ ] 回滚时间评估 < 5分钟

```bash
# 测试回滚流程(可选)
# 1. 记录回滚命令
echo "回滚命令:" > rollback-commands.txt
echo "git reset --hard v1.0.0-backup" >> rollback-commands.txt
echo "docker-compose down && docker-compose up -d" >> rollback-commands.txt

# 2. 验证备份可用
ls -lh /usr/local/bin/go-docker-proxy.v1.0.bak
docker images | grep go-docker-proxy
```

---

## 生产验证

### 18. 真实客户端测试
- [ ] 从不同网络位置测试
- [ ] 不同镜像类型测试
- [ ] 长时间运行稳定性

```bash
# 从不同服务器测试
ssh user@another-server "docker pull registry.w4w.cc:8080/nginx:latest"

# 测试不同镜像
docker pull registry.w4w.cc:8080/redis:latest
docker pull registry.w4w.cc:8080/postgres:latest
docker pull registry.w4w.cc:8080/node:latest
```

### 19. 故障恢复测试
- [ ] 服务重启后正常
- [ ] 缓存恢复正常
- [ ] 统计信息保持

```bash
# 重启测试
docker-compose restart
# 等待 10s
docker-compose ps
curl http://registry.w4w.cc:8080/health

# 缓存验证
curl -I http://registry.w4w.cc:8080/v2/library/nginx/manifests/latest
# 应该: X-Cache: HIT
```

---

## 签署确认

- [ ] 所有测试项通过
- [ ] 性能达到预期
- [ ] 监控数据正常
- [ ] 文档更新完整

**部署人员**: _______________  
**验证人员**: _______________  
**部署时间**: _______________  
**签署时间**: _______________  

---

## 快速参考

### 紧急回滚
```bash
# Docker
docker-compose down && git reset --hard v1.0.0-backup && docker-compose up -d

# 系统服务
sudo systemctl stop go-docker-proxy && \
sudo cp /usr/local/bin/go-docker-proxy.v1.0.bak /usr/local/bin/go-docker-proxy && \
sudo systemctl start go-docker-proxy
```

### 关键命令
```bash
# 服务状态
docker-compose ps  # 或 sudo systemctl status go-docker-proxy

# 查看日志
docker-compose logs -f  # 或 sudo journalctl -u go-docker-proxy -f

# 测试拉取
docker pull registry.w4w.cc:8080/nginx:latest

# 运行测试
./test-aws-redirect.sh registry.w4w.cc:8080
```

### 支持资源
- 📖 [UPGRADE_v1.1.0.md](./UPGRADE_v1.1.0.md)
- 📖 [AWS_REDIRECT_FIX.md](./AWS_REDIRECT_FIX.md)
- 📖 [HOTFIX_SUMMARY.md](./HOTFIX_SUMMARY.md)
- 🛠️ `./test-aws-redirect.sh`
- 🛠️ `./monitor.sh`
