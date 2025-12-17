#!/bin/bash
#
# Docker Registry 代理快速部署脚本
# 适用于: Ubuntu 20.04+, Debian 11+, CentOS 8+
#
set -e

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

echo_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

echo_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# 检测操作系统
detect_os() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        OS=$ID
        VER=$VERSION_ID
    else
        echo_error "无法检测操作系统"
        exit 1
    fi
    echo_info "检测到操作系统: $OS $VER"
}

# 检查是否为root
check_root() {
    if [ "$EUID" -ne 0 ]; then 
        echo_error "请使用 root 权限运行此脚本"
        echo "使用: sudo $0"
        exit 1
    fi
}

# 安装基础依赖
install_dependencies() {
    echo_info "安装基础依赖..."
    
    if [ "$OS" == "ubuntu" ] || [ "$OS" == "debian" ]; then
        apt-get update
        apt-get install -y curl wget git ca-certificates gnupg lsb-release
    elif [ "$OS" == "centos" ] || [ "$OS" == "rhel" ]; then
        yum install -y curl wget git ca-certificates
    fi
}

# 安装 Docker
install_docker() {
    if command -v docker &> /dev/null; then
        echo_info "Docker 已安装: $(docker --version)"
        return
    fi
    
    echo_info "安装 Docker..."
    
    if [ "$OS" == "ubuntu" ] || [ "$OS" == "debian" ]; then
        # 添加 Docker 官方 GPG key
        mkdir -p /etc/apt/keyrings
        curl -fsSL https://download.docker.com/linux/$OS/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
        
        # 设置仓库
        echo \
          "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/$OS \
          $(lsb_release -cs) stable" | tee /etc/apt/sources.list.d/docker.list > /dev/null
        
        # 安装 Docker Engine
        apt-get update
        apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
    elif [ "$OS" == "centos" ] || [ "$OS" == "rhel" ]; then
        yum install -y yum-utils
        yum-config-manager --add-repo https://download.docker.com/linux/centos/docker-ce.repo
        yum install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin
        systemctl start docker
    fi
    
    systemctl enable docker
    echo_info "Docker 安装完成"
}

# 优化网络参数
optimize_network() {
    echo_info "优化网络参数..."
    
    # 备份原始配置
    cp /etc/sysctl.conf /etc/sysctl.conf.bak
    
    # 添加优化参数
    cat >> /etc/sysctl.conf << 'EOF'

# Docker Proxy Network Optimization
# TCP 优化
net.ipv4.tcp_fin_timeout=30
net.ipv4.tcp_keepalive_time=1200
net.ipv4.tcp_tw_reuse=1
net.ipv4.tcp_max_syn_backlog=8192
net.core.somaxconn=65535
net.ipv4.tcp_slow_start_after_idle=0

# BBR 拥塞控制(需要内核 4.9+)
net.ipv4.tcp_congestion_control=bbr
net.core.default_qdisc=fq

# 增加连接追踪表大小
net.netfilter.nf_conntrack_max=1048576
net.nf_conntrack_max=1048576
EOF

    # 应用配置
    sysctl -p
    
    echo_info "网络优化完成"
}

# 配置防火墙
setup_firewall() {
    echo_info "配置防火墙..."
    
    if command -v ufw &> /dev/null; then
        ufw allow 22/tcp
        ufw allow 80/tcp
        ufw allow 443/tcp
        echo "y" | ufw enable
        echo_info "UFW 防火墙配置完成"
    elif command -v firewall-cmd &> /dev/null; then
        firewall-cmd --permanent --add-service=ssh
        firewall-cmd --permanent --add-service=http
        firewall-cmd --permanent --add-service=https
        firewall-cmd --reload
        echo_info "Firewalld 防火墙配置完成"
    else
        echo_warn "未检测到防火墙,请手动配置"
    fi
}

# 安装 Nginx (可选)
install_nginx() {
    read -p "是否安装 Nginx 反向代理? (推荐) [y/N]: " install_nginx_choice
    if [[ ! "$install_nginx_choice" =~ ^[Yy]$ ]]; then
        echo_info "跳过 Nginx 安装"
        return
    fi
    
    echo_info "安装 Nginx..."
    
    if [ "$OS" == "ubuntu" ] || [ "$OS" == "debian" ]; then
        apt-get install -y nginx
    elif [ "$OS" == "centos" ] || [ "$OS" == "rhel" ]; then
        yum install -y nginx
    fi
    
    systemctl enable nginx
    systemctl start nginx
    
    echo_info "Nginx 安装完成"
}

# 安装 Certbot (Let's Encrypt)
install_certbot() {
    read -p "是否安装 Certbot (Let's Encrypt SSL)? [y/N]: " install_certbot_choice
    if [[ ! "$install_certbot_choice" =~ ^[Yy]$ ]]; then
        echo_info "跳过 Certbot 安装"
        return
    fi
    
    echo_info "安装 Certbot..."
    
    if [ "$OS" == "ubuntu" ] || [ "$OS" == "debian" ]; then
        apt-get install -y certbot python3-certbot-nginx
    elif [ "$OS" == "centos" ] || [ "$OS" == "rhel" ]; then
        yum install -y certbot python3-certbot-nginx
    fi
    
    echo_info "Certbot 安装完成"
    echo_warn "请稍后运行: sudo certbot --nginx -d yourdomain.com"
}

# 部署应用
deploy_app() {
    echo_info "配置应用部署..."
    
    # 获取用户输入
    read -p "请输入您的域名 (例如: example.com): " DOMAIN
    read -p "请输入服务端口 (默认: 8080): " PORT
    PORT=${PORT:-8080}
    
    # 创建应用目录
    APP_DIR="/opt/go-docker-proxy"
    mkdir -p $APP_DIR
    cd $APP_DIR
    
    # 创建 docker-compose.yml
    cat > docker-compose.yml << EOF
services:
  go-docker-proxy:
    image: ghcr.io/deyixu/go-docker-proxy:latest
    container_name: go-docker-proxy
    ports:
      - "127.0.0.1:${PORT}:8080"
    environment:
      - PORT=8080
      - CACHE_DIR=/cache
      - DEBUG=false
      - CUSTOM_DOMAIN=${DOMAIN}
      - TARGET_UPSTREAM=https://registry-1.docker.io
    volumes:
      - ./cache:/cache
    restart: unless-stopped
    healthcheck:
      test: ["CMD", "/go-docker-proxy", "-health-check"]
      interval: 30s
      timeout: 3s
      start_period: 5s
      retries: 3
    logging:
      driver: "json-file"
      options:
        max-size: "100m"
        max-file: "3"
EOF

    # 创建缓存目录
    mkdir -p cache
    
    echo_info "应用配置完成"
    echo_info "配置文件位置: $APP_DIR/docker-compose.yml"
}

# 启动服务
start_service() {
    echo_info "启动服务..."
    
    cd /opt/go-docker-proxy
    docker compose up -d
    
    # 等待服务启动
    echo_info "等待服务启动..."
    sleep 5
    
    # 检查服务状态
    if docker compose ps | grep -q "Up"; then
        echo_info "✅ 服务启动成功!"
        docker compose ps
    else
        echo_error "❌ 服务启动失败,请检查日志:"
        docker compose logs
        exit 1
    fi
}

# 健康检查
health_check() {
    echo_info "执行健康检查..."
    
    # 检查端口
    if netstat -tuln | grep -q ":$PORT"; then
        echo_info "✅ 端口 $PORT 正在监听"
    else
        echo_error "❌ 端口 $PORT 未监听"
    fi
    
    # 检查 HTTP 响应
    if curl -s -f http://localhost:$PORT/health > /dev/null; then
        echo_info "✅ 健康检查端点正常"
    else
        echo_error "❌ 健康检查端点异常"
    fi
}

# 创建 Nginx 配置
create_nginx_config() {
    if [ ! -f "/etc/nginx/nginx.conf" ]; then
        echo_info "Nginx 未安装,跳过配置"
        return
    fi
    
    read -p "是否创建 Nginx 配置? [y/N]: " create_nginx_choice
    if [[ ! "$create_nginx_choice" =~ ^[Yy]$ ]]; then
        return
    fi
    
    echo_info "创建 Nginx 配置..."
    
    cat > /etc/nginx/sites-available/docker-proxy << EOF
upstream docker_proxy {
    server 127.0.0.1:${PORT};
    keepalive 32;
}

server {
    listen 80;
    listen [::]:80;
    server_name docker.${DOMAIN} *.${DOMAIN};
    
    location / {
        proxy_pass http://docker_proxy;
        proxy_http_version 1.1;
        proxy_set_header Connection "";
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
        
        # 大文件支持
        client_max_body_size 10G;
        proxy_buffering off;
        proxy_request_buffering off;
        
        # 超时设置
        proxy_connect_timeout 300s;
        proxy_send_timeout 300s;
        proxy_read_timeout 300s;
    }
    
    location /health {
        proxy_pass http://docker_proxy;
        access_log off;
    }
}
EOF

    # 创建符号链接
    ln -sf /etc/nginx/sites-available/docker-proxy /etc/nginx/sites-enabled/
    
    # 测试配置
    nginx -t
    
    # 重载 Nginx
    systemctl reload nginx
    
    echo_info "✅ Nginx 配置完成"
    echo_warn "下一步: 运行 'sudo certbot --nginx -d docker.${DOMAIN}' 配置 SSL"
}

# 显示部署信息
show_deployment_info() {
    echo ""
    echo "======================================"
    echo_info "🎉 部署完成!"
    echo "======================================"
    echo ""
    echo "📝 部署信息:"
    echo "  域名: ${DOMAIN}"
    echo "  端口: ${PORT}"
    echo "  应用目录: /opt/go-docker-proxy"
    echo ""
    echo "🔍 常用命令:"
    echo "  查看日志: cd /opt/go-docker-proxy && docker compose logs -f"
    echo "  重启服务: cd /opt/go-docker-proxy && docker compose restart"
    echo "  停止服务: cd /opt/go-docker-proxy && docker compose down"
    echo "  更新服务: cd /opt/go-docker-proxy && docker compose pull && docker compose up -d"
    echo ""
    echo "🧪 测试命令:"
    echo "  健康检查: curl http://localhost:${PORT}/health"
    echo "  Docker API: curl http://localhost:${PORT}/v2/"
    echo ""
    echo "🌐 下一步:"
    echo "  1. 配置 DNS: 添加 A 记录 docker.${DOMAIN} 指向服务器 IP"
    echo "  2. 配置 SSL: sudo certbot --nginx -d docker.${DOMAIN}"
    echo "  3. 测试访问: docker pull docker.${DOMAIN}/library/alpine:latest"
    echo ""
    echo "📚 更多信息: 查看 DEPLOYMENT_CN.md"
    echo "======================================"
}

# 主流程
main() {
    echo "======================================"
    echo "  Docker Registry 代理 - 境外部署脚本"
    echo "======================================"
    echo ""
    
    check_root
    detect_os
    install_dependencies
    install_docker
    optimize_network
    setup_firewall
    install_nginx
    install_certbot
    deploy_app
    start_service
    health_check
    create_nginx_config
    show_deployment_info
}

# 运行主流程
main
