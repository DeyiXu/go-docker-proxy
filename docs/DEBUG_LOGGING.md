# 调试日志说明文档

## 概述

为了方便 DEBUG 调试,在关键函数中添加了详细的日志输出。所有调试日志都带有 `[DEBUG]` 前缀,只在 `DEBUG=true` 时输出。

## 启用调试模式

```bash
# 环境变量方式
export DEBUG=true
./go-docker-proxy

# 或 Docker Compose
DEBUG=true docker-compose up

# 或在 .env 文件中
DEBUG=true
```

## 日志输出位置

### 1. 服务启动日志

**位置**: `Start()` 函数

**输出内容**:
```
[DEBUG] Debug mode enabled
Starting proxy server on port 8080
Custom domain: example.com
Cache directory: ./cache
Debug mode: true
[DEBUG] Available routes:
  docker.example.com -> https://registry-1.docker.io
  quay.example.com -> https://quay.io
  ...
```

**用途**: 
- 确认调试模式已启用
- 查看所有路由配置
- 验证端口和缓存目录配置

---

### 2. 路由匹配日志

**位置**: `routeByHost()` 函数

**输出内容**:
```
[DEBUG] Route matched: docker.example.com:8080 -> https://registry-1.docker.io
[DEBUG] No route found for host: unknown.example.com
[DEBUG] 使用 TARGET_UPSTREAM: https://registry-1.docker.io
```

**用途**:
- 检查请求的 Host 是否匹配到正确的上游
- 排查路由配置问题
- 调试 TARGET_UPSTREAM 环境变量

---

### 3. /v2/ 端点日志

**位置**: `handleV2Root()` 函数

**输出内容**:
```
[DEBUG] No upstream found for host: test.com
[DEBUG] /v2/ request - Host: docker.example.com:8080, Upstream: https://registry-1.docker.io
[DEBUG] /v2/ RoundTrip error: dial tcp: connection refused
[DEBUG] /v2/ response status: 401
[DEBUG] /v2/ returning 401 auth challenge
```

**用途**:
- 检查 /v2/ 认证流程
- 排查上游连接问题
- 验证 401 认证挑战是否正确返回

---

### 4. 认证端点日志

**位置**: `handleAuth()` 函数

**输出内容**:
```
[DEBUG] /v2/auth - No upstream found for host: test.com
[DEBUG] /v2/auth - Host: docker.example.com, Upstream: https://registry-1.docker.io, Scope: repository:nginx:pull
[DEBUG] /v2/auth RoundTrip error: connection timeout
[DEBUG] /v2/auth unexpected status: 200
[DEBUG] /v2/auth missing WWW-Authenticate header
[DEBUG] /v2/auth WWW-Authenticate: Bearer realm="https://auth.docker.io/token",service="registry.docker.io"
[DEBUG] /v2/auth parse error: invalid format
[DEBUG] /v2/auth scope rewritten: repository:nginx:pull -> repository:library/nginx:pull
[DEBUG] /v2/auth token fetch error: network error
[DEBUG] /v2/auth token fetched successfully, status: 200
```

**用途**:
- 跟踪完整的认证流程
- 检查 scope 是否正确处理
- 排查 token 获取失败问题
- 验证 Docker Hub library 镜像的 scope 重写

---

### 5. V2 请求处理日志

**位置**: `handleV2Request()` 函数

**输出内容**:
```
[DEBUG] /v2/* No upstream found for host: test.com, path: /v2/nginx/manifests/latest
[DEBUG] /v2/* Request - Method: GET, Host: docker.example.com, Path: /v2/nginx/manifests/latest, Upstream: https://registry-1.docker.io
[DEBUG] /v2/* Library redirect: /v2/nginx/manifests/latest -> /v2/library/nginx/manifests/latest
[DEBUG] /v2/* Cache HIT: /v2/library/nginx/manifests/latest
[DEBUG] /v2/* Cache MISS: /v2/library/nginx/manifests/latest
```

**用途**:
- 跟踪每个请求的详细信息
- 检查 library 重定向是否生效
- 查看缓存命中情况
- 排查路径处理问题

---

### 6. 代理请求日志

**位置**: `proxyRequestWithRoundTrip()` 函数

**输出内容**:
```
[DEBUG] Proxy request to: https://registry-1.docker.io/v2/library/nginx/manifests/latest
[DEBUG] Proxy RoundTrip error: connection refused
[DEBUG] Proxy response status: 200 from registry-1.docker.io
[DEBUG] Proxy got 401, returning auth challenge
[DEBUG] Proxy got redirect 307 to: https://production.cloudflare.docker.com/...
[DEBUG] External storage detected (production.cloudflare.docker.com), returning redirect to client
[DEBUG] Docker Hub internal redirect, following server-side
```

**用途**:
- 跟踪代理请求到上游的过程
- 检查重定向处理逻辑
- 验证外部存储重定向是否正确识别
- 排查 AWS S3 重定向问题

---

### 7. Docker Hub 特殊处理日志

**位置**: `processDockerHubLibraryRedirect()` 和 `processDockerHubScope()` 函数

**输出内容**:
```
[DEBUG] Docker Hub library redirect: /v2/nginx/manifests/latest -> /v2/library/nginx/manifests/latest
[DEBUG] Docker Hub scope rewrite: repository:nginx:pull -> repository:library/nginx:pull
```

**用途**:
- 验证 Docker Hub library 镜像路径重写
- 检查 scope 重写逻辑
- 排查 library 镜像访问问题

---

## 日志级别说明

### DEBUG 日志
- **前缀**: `[DEBUG]`
- **条件**: `DEBUG=true`
- **用途**: 详细的调试信息,包括每个请求的流程、参数、结果

### INFO 日志
- **无前缀** 或 **标准日志格式**
- **条件**: 始终输出
- **用途**: 服务启动信息、配置信息、重要事件

### ERROR 日志
- **包含 `error`** 关键字
- **条件**: 始终输出
- **用途**: 错误信息、异常情况

---

## 典型调试场景

### 场景 1: 镜像拉取失败

**步骤**:
1. 启用 DEBUG 模式
2. 执行: `docker pull docker.example.com:8080/nginx:latest`
3. 查看日志输出

**关键日志**:
```
[DEBUG] /v2/* Request - Method: GET, Path: /v2/nginx/manifests/latest
[DEBUG] /v2/* Library redirect: /v2/nginx/manifests/latest -> /v2/library/nginx/manifests/latest
[DEBUG] Proxy request to: https://registry-1.docker.io/v2/library/nginx/manifests/latest
[DEBUG] Proxy response status: 200
[DEBUG] Proxy got redirect 307 to: https://production.cloudflare.docker.com/...
[DEBUG] External storage detected, returning redirect to client
```

### 场景 2: 认证失败

**关键日志**:
```
[DEBUG] /v2/auth - Scope: repository:nginx:pull
[DEBUG] /v2/auth scope rewritten: repository:nginx:pull -> repository:library/nginx:pull
[DEBUG] /v2/auth WWW-Authenticate: Bearer realm="https://auth.docker.io/token"...
[DEBUG] /v2/auth token fetched successfully, status: 200
```

### 场景 3: 路由不匹配

**关键日志**:
```
[DEBUG] No route found for host: wrong.domain.com
[DEBUG] /v2/* No upstream found for host: wrong.domain.com
```

### 场景 4: 缓存问题

**关键日志**:
```
[DEBUG] /v2/* Cache MISS: /v2/library/nginx/manifests/latest
[DEBUG] Proxy request to: https://registry-1.docker.io/...
[DEBUG] Proxy response status: 200
# 第二次请求
[DEBUG] /v2/* Cache HIT: /v2/library/nginx/manifests/latest
```

---

## 性能影响

### DEBUG 模式关闭 (生产环境)
- **性能影响**: 无
- **日志量**: 最小
- **适用**: 生产环境

### DEBUG 模式开启 (开发/测试)
- **性能影响**: 轻微 (每个请求增加几次 log.Printf 调用)
- **日志量**: 大 (每个请求 5-15 行日志)
- **适用**: 开发、测试、故障排查

**建议**:
- 生产环境默认关闭 DEBUG
- 遇到问题时临时开启,排查后关闭
- 使用日志收集工具 (如 ELK) 管理大量日志

---

## 日志查看命令

### Docker Compose
```bash
# 实时查看所有日志
docker-compose logs -f

# 只看最近 100 行
docker-compose logs --tail 100

# 只看 DEBUG 日志
docker-compose logs | grep DEBUG

# 保存到文件
docker-compose logs > debug.log
```

### 系统服务
```bash
# 实时查看
sudo journalctl -u go-docker-proxy -f

# 只看最近 100 行
sudo journalctl -u go-docker-proxy -n 100

# 只看 DEBUG 日志
sudo journalctl -u go-docker-proxy | grep DEBUG

# 查看最近 1 小时
sudo journalctl -u go-docker-proxy --since "1 hour ago"
```

### 直接运行
```bash
# 运行并保存日志
DEBUG=true ./go-docker-proxy 2>&1 | tee debug.log

# 只看 DEBUG 日志
DEBUG=true ./go-docker-proxy 2>&1 | grep DEBUG
```

---

## 日志分析技巧

### 1. 跟踪单个请求
使用请求 ID (如果启用了 RequestID 中间件):
```bash
docker-compose logs | grep "request-id-12345"
```

### 2. 统计错误
```bash
docker-compose logs | grep -i error | wc -l
```

### 3. 查看缓存命中率
```bash
# 统计 HIT
docker-compose logs | grep "Cache HIT" | wc -l

# 统计 MISS
docker-compose logs | grep "Cache MISS" | wc -l
```

### 4. 查看重定向情况
```bash
docker-compose logs | grep "redirect"
```

### 5. 分析慢请求
结合 chi 的 Logger 中间件,查看响应时间。

---

## 故障排查流程

### 1. 确认服务运行
```bash
curl http://localhost:8080/health
```

### 2. 启用 DEBUG
```bash
export DEBUG=true
docker-compose restart
```

### 3. 重现问题
```bash
docker pull docker.example.com:8080/nginx:latest
```

### 4. 查看日志
```bash
docker-compose logs --tail 200 | grep DEBUG
```

### 5. 分析日志
- 检查路由匹配
- 检查上游连接
- 检查认证流程
- 检查重定向处理
- 检查缓存状态

### 6. 修复后验证
```bash
# 清理缓存
rm -rf cache/*

# 重启服务
docker-compose restart

# 重新测试
docker pull docker.example.com:8080/nginx:latest
```

---

## 相关工具

### 监控脚本
```bash
./monitor.sh  # 选择 "1) 实时监控"
```

### 测试脚本
```bash
./test-aws-redirect.sh localhost:8080
```

### 性能测试
```bash
./monitor.sh  # 选择 "2) 性能测试"
```

---

## 日志示例

### 完整请求流程日志
```
2024-12-14 12:00:01 [DEBUG] /v2/* Request - Method: GET, Host: docker.example.com:8080, Path: /v2/nginx/manifests/latest, Upstream: https://registry-1.docker.io
2024-12-14 12:00:01 [DEBUG] Route matched: docker.example.com:8080 -> https://registry-1.docker.io
2024-12-14 12:00:01 [DEBUG] /v2/* Library redirect: /v2/nginx/manifests/latest -> /v2/library/nginx/manifests/latest
2024-12-14 12:00:02 [DEBUG] /v2/* Cache MISS: /v2/library/nginx/manifests/latest
2024-12-14 12:00:02 [DEBUG] Proxy request to: https://registry-1.docker.io/v2/library/nginx/manifests/latest
2024-12-14 12:00:03 [DEBUG] Proxy response status: 200 from registry-1.docker.io
```

---

## 总结

通过这套完整的调试日志系统,你可以:

✅ 跟踪每个请求的完整流程  
✅ 快速定位问题所在  
✅ 验证功能是否按预期工作  
✅ 排查生产环境问题  
✅ 优化性能和缓存策略  

**记住**: 生产环境默认关闭 DEBUG,需要时临时开启!
