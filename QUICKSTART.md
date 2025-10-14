# 境外部署快速开始 🚀

## 概述

本指南将帮助您在 **10分钟内** 完成境外服务器部署,确保中国大陆用户能够高速访问您的 Docker Registry 代理服务。

## 前提条件

- ✅ 一台境外服务器(推荐: 香港/新加坡/东京)
- ✅ 服务器配置: 最低 1C2G, 推荐 2C4G
- ✅ 操作系统: Ubuntu 20.04+, Debian 11+, 或 CentOS 8+
- ✅ 一个域名(用于配置子域名)
- ✅ Root 或 sudo 权限

## 🎯 方案一: 一键自动部署(推荐)

### 步骤 1: 下载项目

```bash
# SSH 连接到服务器后执行
git clone https://github.com/DeyiXu/go-docker-proxy.git
cd go-docker-proxy
```

### 步骤 2: 运行一键部署脚本

```bash
sudo ./deploy-overseas.sh
```

脚本会自动完成:
- ✅ 检测操作系统
- ✅ 安装 Docker 和依赖
- ✅ 优化网络参数(BBR拥塞控制)
- ✅ 配置防火墙
- ✅ 部署应用容器
- ✅ 可选安装 Nginx + Certbot(SSL)

### 步骤 3: 配置 DNS

在您的域名提供商处添加 A 记录:

```
docker.yourdomain.com    A    <服务器公网IP>
quay.yourdomain.com      A    <服务器公网IP>
gcr.yourdomain.com       A    <服务器公网IP>
ghcr.yourdomain.com      A    <服务器公网IP>
k8s.yourdomain.com       A    <服务器公网IP>
```

**提示**: 如果要支持所有子域名,可以添加通配符:
```
*.yourdomain.com         A    <服务器公网IP>
```

### 步骤 4: 配置 SSL 证书(推荐)

```bash
# 安装 Certbot (如果脚本中未选择安装)
sudo apt-get install certbot python3-certbot-nginx

# 为主域名申请证书
sudo certbot --nginx -d docker.yourdomain.com

# 为所有子域名申请证书(如果使用通配符DNS)
sudo certbot certonly --manual --preferred-challenges dns -d "*.yourdomain.com"
```

### 步骤 5: 测试部署

```bash
# 检查服务状态
cd /opt/go-docker-proxy
docker compose ps

# 查看日志
docker compose logs -f

# 测试健康检查
curl http://localhost:8080/health

# 测试 Registry API
curl http://localhost:8080/v2/
```

### 步骤 6: 测试 Docker Pull

在**中国大陆**的机器上测试:

```bash
# 测试拉取镜像
docker pull docker.yourdomain.com/library/alpine:latest

# 测试其他仓库
docker pull quay.yourdomain.com/prometheus/prometheus:latest
docker pull ghcr.yourdomain.com/linuxserver/nginx:latest
```

### 完成 🎉

您的 Docker Registry 代理已成功部署!

---

## 🛠️ 方案二: 手动部署(自定义配置)

### 步骤 1: 安装 Docker

#### Ubuntu/Debian:
```bash
sudo apt-get update
sudo apt-get install -y docker.io docker-compose-plugin
sudo systemctl enable docker
sudo systemctl start docker
```

#### CentOS/RHEL:
```bash
sudo yum install -y docker docker-compose-plugin
sudo systemctl enable docker
sudo systemctl start docker
```

### 步骤 2: 优化网络(可选但推荐)

```bash
# 启用 BBR 拥塞控制
sudo bash -c 'cat >> /etc/sysctl.conf << EOF
net.ipv4.tcp_congestion_control=bbr
net.core.default_qdisc=fq
net.ipv4.tcp_slow_start_after_idle=0
net.ipv4.tcp_keepalive_time=1200
EOF'

sudo sysctl -p
```

### 步骤 3: 部署应用

```bash
# 创建应用目录
sudo mkdir -p /opt/go-docker-proxy
cd /opt/go-docker-proxy

# 创建 docker-compose.yml
cat > docker-compose.yml << 'EOF'
services:
  go-docker-proxy:
    image: ghcr.io/deyixu/go-docker-proxy:latest
    container_name: go-docker-proxy
    ports:
      - "8080:8080"
    environment:
      - PORT=8080
      - CACHE_DIR=/cache
      - DEBUG=false
      - CUSTOM_DOMAIN=yourdomain.com  # 修改为你的域名
    volumes:
      - ./cache:/cache
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "/go-docker-proxy", "-health-check"]
      interval: 30s
      timeout: 3s
      retries: 3
    logging:
      driver: "json-file"
      options:
        max-size: "100m"
        max-file: "3"
EOF

# 启动服务
docker compose up -d

# 查看日志
docker compose logs -f
```

### 步骤 4: 配置 Nginx(推荐)

```bash
# 安装 Nginx
sudo apt-get install -y nginx

# 创建配置文件
sudo tee /etc/nginx/sites-available/docker-proxy << 'EOF'
upstream docker_proxy {
    server 127.0.0.1:8080;
    keepalive 32;
}

server {
    listen 80;
    server_name docker.yourdomain.com *.yourdomain.com;
    
    location / {
        proxy_pass http://docker_proxy;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        
        client_max_body_size 10G;
        proxy_buffering off;
    }
}
EOF

# 启用配置
sudo ln -sf /etc/nginx/sites-available/docker-proxy /etc/nginx/sites-enabled/
sudo nginx -t
sudo systemctl reload nginx
```

### 步骤 5: 配置 SSL

```bash
sudo apt-get install -y certbot python3-certbot-nginx
sudo certbot --nginx -d docker.yourdomain.com
```

### 步骤 6: 配置防火墙

```bash
sudo ufw allow 22/tcp
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw enable
```

---

## 📊 部署后监控

### 启动实时监控

```bash
cd /opt/go-docker-proxy
# 下载监控脚本
curl -O https://raw.githubusercontent.com/DeyiXu/go-docker-proxy/main/monitor.sh
chmod +x monitor.sh

# 运行监控
./monitor.sh -m  # 持续监控模式
```

### 查看服务状态

```bash
# 单次健康检查
./monitor.sh -c

# 查看日志
./monitor.sh -l 100

# 性能测试
./monitor.sh -p
```

---

## 🚀 性能优化(进阶)

### 1. 启用 Cloudflare CDN

1. 将域名托管到 Cloudflare
2. 添加 DNS A 记录
3. 启用 Proxy(橙色云朵)
4. SSL/TLS 设置: Full (strict)

### 2. 配置缓存规则

在 Cloudflare 页面规则中添加:

```
规则1: *.yourdomain.com/v2/*/blobs/*
  - 缓存级别: 全部缓存
  - 边缘缓存 TTL: 7天

规则2: *.yourdomain.com/v2/*/manifests/*
  - 缓存级别: 全部缓存
  - 边缘缓存 TTL: 1小时
```

### 3. 监控和告警

使用 UptimeRobot 免费监控:
- URL: `https://docker.yourdomain.com/health`
- 检查间隔: 5分钟
- 告警: Email/Telegram

---

## 🔧 常见问题

### Q1: 部署后无法访问?

```bash
# 检查服务是否运行
docker ps

# 检查端口是否监听
sudo netstat -tuln | grep 8080

# 检查防火墙
sudo ufw status

# 查看日志
docker logs go-docker-proxy
```

### Q2: Docker pull 很慢?

1. 检查服务器带宽
2. 启用 Cloudflare CDN
3. 查看 [网络优化配置](./NETWORK_OPTIMIZATION.md)
4. 考虑部署到更近的地区(香港)

### Q3: SSL 证书配置失败?

```bash
# 确认域名解析正确
dig docker.yourdomain.com

# 使用 DNS 验证方式
sudo certbot certonly --manual --preferred-challenges dns \
  -d docker.yourdomain.com
```

### Q4: 如何更新服务?

```bash
cd /opt/go-docker-proxy
docker compose pull
docker compose up -d
```

### Q5: 如何查看缓存使用情况?

```bash
# 查看缓存大小
du -sh ./cache

# 查看缓存文件数
find ./cache -type f | wc -l

# 清理缓存
rm -rf ./cache/*
```

---

## 📈 性能基准

基于实际部署经验:

| 部署地区 | 中国大陆延迟 | 下载速度 | 月度成本 |
|---------|-------------|---------|---------|
| 香港     | 20-50ms     | 10-50MB/s | $30-50 |
| 新加坡   | 60-100ms    | 5-30MB/s  | $20-40 |
| 东京     | 80-120ms    | 5-20MB/s  | $20-40 |

**硬件配置**: 2C4G, 100GB SSD, 1TB 流量

---

## 📚 更多资源

- **[完整部署指南](./DEPLOYMENT_CN.md)** - 详细的部署文档
- **[网络优化配置](./NETWORK_OPTIMIZATION.md)** - 深度网络优化
- **[架构文档](./ARCHITECTURE.md)** - 系统架构说明
- **[GitHub Issues](https://github.com/DeyiXu/go-docker-proxy/issues)** - 问题反馈

---

## 🎉 部署成功后

恭喜! 您现在拥有了一个高性能的 Docker Registry 代理服务。

**下一步**:
1. ✅ 配置客户端使用代理
2. ✅ 设置监控和告警
3. ✅ 定期查看日志和性能指标
4. ✅ 根据使用情况调整缓存大小

**使用示例**:
```bash
# 在中国大陆的机器上配置
sudo tee /etc/docker/daemon.json << EOF
{
  "registry-mirrors": [
    "https://docker.yourdomain.com"
  ]
}
EOF

sudo systemctl restart docker

# 测试
docker pull nginx:latest
```

享受高速的 Docker 镜像下载体验! 🚀
