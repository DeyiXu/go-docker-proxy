# 全球部署优化指南

## 概述

本指南提供详细的部署方案,帮助您在全球范围内部署服务,确保各地用户都能获得良好的访问体验。

## 核心优化策略

### 1. 网络层优化

#### 1.1 选择合适的部署地区

**推荐地区**(按网络性能排序):

1. **亚太区域 A** - 延迟低(20-50ms)
   - AWS ap-east-1
   - Azure East Asia
   - Google Cloud asia-east2
   - 其他主流云服务商亚太节点

2. **亚太区域 B** - 延迟适中(60-100ms),网络稳定
   - AWS ap-southeast-1
   - Azure Southeast Asia
   - Google Cloud asia-southeast1

3. **亚太区域 C** - 延迟可接受(80-120ms)
   - AWS ap-northeast-1
   - Azure Japan East
   - Google Cloud asia-northeast1

4. **美洲西部** - 备选方案(150-200ms)
   - AWS us-west-1/2
   - Azure West US
   - Google Cloud us-west1

**性能考虑**:
- 欧洲区域(延迟较高 250ms+)
- 大洋洲区域(延迟高且不稳定)

#### 1.2 TCP/网络优化

已在代码中实现的优化:

```go
transport := &http.Transport{
    MaxIdleConns:          100,              // 增加连接池
    MaxIdleConnsPerHost:   20,               // 每个host保持20个连接
    MaxConnsPerHost:       50,               // 每个host最多50个连接
    IdleConnTimeout:       90 * time.Second, // 连接保活
    TLSHandshakeTimeout:   10 * time.Second, // TLS握手超时
    ExpectContinueTimeout: 1 * time.Second,
    
    // 启用 HTTP/2
    ForceAttemptHTTP2: true,
    
    // 禁用压缩,减少CPU开销
    DisableCompression: true,
}
```

#### 1.3 建议的系统级网络优化

创建优化配置脚本:

```bash
# 优化TCP参数(部署到服务器后执行)
cat > optimize-network.sh << 'EOF'
#!/bin/bash

# TCP优化
sysctl -w net.ipv4.tcp_fin_timeout=30
sysctl -w net.ipv4.tcp_keepalive_time=1200
sysctl -w net.ipv4.tcp_tw_reuse=1
sysctl -w net.ipv4.tcp_max_syn_backlog=8192
sysctl -w net.core.somaxconn=65535
sysctl -w net.ipv4.tcp_slow_start_after_idle=0

# BBR拥塞控制(如果内核支持)
if lsmod | grep -q tcp_bbr; then
    sysctl -w net.ipv4.tcp_congestion_control=bbr
    sysctl -w net.core.default_qdisc=fq
fi

# 持久化配置
cat >> /etc/sysctl.conf << 'SYSCTL'
# Docker Proxy Network Optimization
net.ipv4.tcp_fin_timeout=30
net.ipv4.tcp_keepalive_time=1200
net.ipv4.tcp_tw_reuse=1
net.ipv4.tcp_max_syn_backlog=8192
net.core.somaxconn=65535
net.ipv4.tcp_slow_start_after_idle=0
net.ipv4.tcp_congestion_control=bbr
net.core.default_qdisc=fq
SYSCTL

sysctl -p
EOF

chmod +x optimize-network.sh
sudo ./optimize-network.sh
```

### 2. DNS 优化

#### 2.1 使用多个DNS提供商

配置多个DNS A记录,实现就近访问:

```
docker.yourdomain.com  A  <亚太A区服务器IP>     TTL 300
docker.yourdomain.com  A  <新加坡服务器IP>   TTL 300
docker.yourdomain.com  A  <东京服务器IP>     TTL 300
```

#### 2.2 智能DNS解析(推荐)

使用支持分地区解析的DNS服务:

- **Cloudflare**: 免费,支持地理位置路由
- **AWS Route 53**: 支持延迟路由策略
- **DNSPod(国际版)**: 专门优化目标区域访问

配置示例(Cloudflare):
```
1. 添加多个A记录到不同地区服务器
2. 启用 Load Balancing (负载均衡)
3. 设置健康检查: /health
4. 启用 Geo Steering (地理位置路由)
```

### 3. CDN加速(可选但强烈推荐)

#### 3.1 Cloudflare CDN

**优势**:
- 免费
- 在目标区域有多个节点
- 自动启用 HTTP/2/3
- DDoS 防护

**配置步骤**:

1. 添加域名到 Cloudflare
2. 配置 DNS 记录
3. 开启 Proxy (橙色云朵图标)
4. SSL/TLS 设置为 "Full" 或 "Full (strict)"
5. 优化设置:
   ```
   Speed > Optimization
   - Auto Minify: 关闭(二进制数据不需要)
   - Brotli: 开启
   - Early Hints: 开启
   - HTTP/2: 开启
   - HTTP/3: 开启
   ```

#### 3.2 Cloudflare 配置示例

创建 Cloudflare Worker 配置文件:

```javascript
// cloudflare-worker.js (可选的额外优化层)
addEventListener('fetch', event => {
  event.respondWith(handleRequest(event.request))
})

async function handleRequest(request) {
  const url = new URL(request.url)
  
  // 添加缓存控制头
  const response = await fetch(request)
  const newResponse = new Response(response.body, response)
  
  // 对 blobs 启用长期缓存
  if (url.pathname.includes('/blobs/')) {
    newResponse.headers.set('Cache-Control', 'public, max-age=604800') // 7天
  }
  
  // 对 manifests 使用短期缓存
  if (url.pathname.includes('/manifests/')) {
    newResponse.headers.set('Cache-Control', 'public, max-age=3600') // 1小时
  }
  
  return newResponse
}
```

### 4. 服务配置优化

#### 4.1 Docker Compose 生产配置

更新 `docker-compose.yml`:

```yaml
services:
  go-docker-proxy:
    build: .
    ports:
      - "8080:8080"
    environment:
      - PORT=8080
      - CACHE_DIR=/cache
      - DEBUG=false  # 生产环境关闭调试
      - CUSTOM_DOMAIN=yourdomain.com
      - TARGET_UPSTREAM=https://registry-1.docker.io
    volumes:
      - cache_data:/cache
    restart: unless-stopped
    
    # 资源限制(根据服务器配置调整)
    deploy:
      resources:
        limits:
          cpus: '2'
          memory: 4G
        reservations:
          cpus: '1'
          memory: 2G
    
    # 健康检查
    healthcheck:
      test: ["CMD", "/go-docker-proxy", "-health-check"]
      interval: 30s
      timeout: 3s
      start_period: 5s
      retries: 3
    
    # 日志配置
    logging:
      driver: "json-file"
      options:
        max-size: "100m"
        max-file: "3"

volumes:
  cache_data:
    driver: local
```

#### 4.2 Nginx 反向代理(推荐)

在服务器前端放置 Nginx 以获得更好的性能:

```nginx
# /etc/nginx/sites-available/docker-proxy
upstream docker_proxy {
    server 127.0.0.1:8080;
    keepalive 32;
}

server {
    listen 80;
    listen [::]:80;
    server_name docker.yourdomain.com *.yourdomain.com;
    
    # 重定向到 HTTPS
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl http2;
    listen [::]:443 ssl http2;
    server_name docker.yourdomain.com *.yourdomain.com;
    
    # SSL 证书(使用 Let's Encrypt)
    ssl_certificate /etc/letsencrypt/live/yourdomain.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/yourdomain.com/privkey.pem;
    
    # SSL 优化配置
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_ciphers 'ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES256-GCM-SHA384:ECDHE-RSA-AES256-GCM-SHA384';
    ssl_prefer_server_ciphers off;
    ssl_session_cache shared:SSL:10m;
    ssl_session_timeout 10m;
    
    # 安全头
    add_header Strict-Transport-Security "max-age=31536000; includeSubDomains" always;
    add_header X-Content-Type-Options "nosniff" always;
    add_header X-Frame-Options "DENY" always;
    
    # 客户端最大body大小(Docker layers可能很大)
    client_max_body_size 10G;
    client_body_buffer_size 128k;
    
    # 超时设置
    proxy_connect_timeout 300s;
    proxy_send_timeout 300s;
    proxy_read_timeout 300s;
    send_timeout 300s;
    
    # 代理配置
    location / {
        proxy_pass http://docker_proxy;
        proxy_http_version 1.1;
        
        # 连接保活
        proxy_set_header Connection "";
        
        # 传递原始请求信息
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        
        # 禁用缓冲(流式传输大文件)
        proxy_buffering off;
        proxy_request_buffering off;
    }
    
    # 健康检查
    location /health {
        proxy_pass http://docker_proxy;
        access_log off;
    }
}
```

### 5. 监控和告警

#### 5.1 添加监控端点

在 `main.go` 中已有 `/health` 端点,建议添加详细的统计信息:

```go
// 在 main.go 中添加统计端点
r.Get("/stats", p.handleStats)

func (p *ProxyServer) handleStats(w http.ResponseWriter, r *http.Request) {
    stats := p.cache.GetStats()
    w.Header().Set("Content-Type", "application/json")
    json.NewEncoder(w).Encode(map[string]interface{}{
        "cache": stats,
        "uptime": time.Since(startTime).String(),
        "goroutines": runtime.NumGoroutine(),
    })
}
```

#### 5.2 使用 UptimeRobot 监控

免费监控服务,每5分钟检查一次:

1. 访问 https://uptimerobot.com
2. 添加 HTTP(s) 监控
3. URL: `https://docker.yourdomain.com/health`
4. 设置告警(Email/Telegram/Slack)

### 6. 部署检查清单

部署完成后,使用以下清单验证:

```bash
#!/bin/bash
# deployment-check.sh

DOMAIN="docker.yourdomain.com"

echo "=== 1. DNS 解析检查 ==="
dig +short $DOMAIN

echo -e "\n=== 2. HTTPS 证书检查 ==="
echo | openssl s_client -servername $DOMAIN -connect $DOMAIN:443 2>/dev/null | openssl x509 -noout -dates

echo -e "\n=== 3. 健康检查 ==="
curl -s -o /dev/null -w "%{http_code}" https://$DOMAIN/health
echo ""

echo -e "\n=== 4. Docker Registry V2 API 检查 ==="
curl -s -o /dev/null -w "%{http_code}" https://$DOMAIN/v2/
echo ""

echo -e "\n=== 5. 延迟测试(从目标区域) ==="
ping -c 4 $DOMAIN

echo -e "\n=== 6. HTTP/2 支持检查 ==="
curl -I --http2 https://$DOMAIN/health 2>&1 | grep "HTTP/2"

echo -e "\n=== 7. 实际 Docker Pull 测试 ==="
docker pull $DOMAIN/library/alpine:latest

echo -e "\n=== 部署检查完成 ==="
```

### 7. 客户端使用配置

#### 7.1 配置 Docker daemon

在目标区域的服务器上配置:

```bash
# /etc/docker/daemon.json
{
  "registry-mirrors": [
    "https://docker.yourdomain.com"
  ],
  "insecure-registries": [],
  "log-driver": "json-file",
  "log-opts": {
    "max-size": "100m",
    "max-file": "3"
  }
}

# 重启 Docker
sudo systemctl daemon-reload
sudo systemctl restart docker
```

#### 7.2 验证配置

```bash
# 查看配置
docker info | grep -A 5 "Registry Mirrors"

# 测试拉取
docker pull docker.yourdomain.com/library/nginx:latest

# 比较速度
time docker pull docker.io/library/alpine:latest
time docker pull docker.yourdomain.com/library/alpine:latest
```

## 性能基准

根据实际部署经验,以下是不同地区的参考性能:

| 部署地区 | 延迟(目标区域) | 下载速度 | 推荐度 |
|---------|-------------|---------|--------|
| 亚太A区     | 20-50ms     | 10-50MB/s | ⭐⭐⭐⭐⭐ |
| 新加坡   | 60-100ms    | 5-30MB/s  | ⭐⭐⭐⭐ |
| 东京     | 80-120ms    | 5-20MB/s  | ⭐⭐⭐⭐ |
| 美西     | 150-200ms   | 2-10MB/s  | ⭐⭐⭐ |
| 欧洲     | 250-350ms   | 1-5MB/s   | ⭐⭐ |

## 故障排查

### 问题1: 访问超时

```bash
# 检查防火墙
sudo ufw status
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp

# 检查服务状态
docker ps
docker logs go-docker-proxy
```

### 问题2: SSL 证书问题

```bash
# 使用 Let's Encrypt 自动续期
sudo certbot renew --dry-run
```

### 问题3: 速度慢

1. 检查服务器带宽
2. 启用 Cloudflare CDN
3. 优化缓存配置
4. 考虑部署到更近的地区

## 成本估算

基于亚太A区部署的月度成本参考:

| 服务 | 配置 | 月费用 | 备注 |
|-----|------|--------|------|
| VPS | 2C4G 100GB | $20-40 | 亚太A区地区 |
| 流量 | 1TB/月 | $10-20 | 超出部分 |
| 域名 | .com | $12/年 | |
| SSL | Let's Encrypt | 免费 | |
| CDN | Cloudflare | 免费 | 基础版 |
| **总计** | | **$30-60/月** | |

## 总结

要确保远程部署的 Docker Registry 代理能被目标区域正常访问,关键在于:

1. ✅ **选择合适的地理位置**(亚太A区 > 新加坡 > 东京)
2. ✅ **使用 CDN 加速**(Cloudflare 免费且有效)
3. ✅ **优化网络配置**(BBR、连接池、HTTP/2)
4. ✅ **智能DNS解析**(多地区服务器+就近访问)
5. ✅ **监控和维护**(UptimeRobot + 日志分析)

本项目的代码已经针对性能进行了优化,只需按照本指南进行部署配置即可获得最佳效果。
