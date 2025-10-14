#!/bin/bash
#
# 服务监控脚本 - 用于监控 Docker Registry 代理服务
#
set -e

# 配置
DOMAIN="${DOMAIN:-localhost}"
PORT="${PORT:-8080}"
CHECK_INTERVAL=30  # 检查间隔(秒)

# 颜色
GREEN='\033[0;32m'
RED='\033[0;31m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

# 日志函数
log_info() {
    echo -e "${BLUE}[$(date +'%Y-%m-%d %H:%M:%S')]${NC} ${GREEN}[INFO]${NC} $1"
}

log_warn() {
    echo -e "${BLUE}[$(date +'%Y-%m-%d %H:%M:%S')]${NC} ${YELLOW}[WARN]${NC} $1"
}

log_error() {
    echo -e "${BLUE}[$(date +'%Y-%m-%d %H:%M:%S')]${NC} ${RED}[ERROR]${NC} $1"
}

# 检查服务是否运行
check_service_running() {
    if docker ps | grep -q go-docker-proxy; then
        return 0
    else
        return 1
    fi
}

# 检查端口是否监听
check_port_listening() {
    if nc -z localhost $PORT 2>/dev/null; then
        return 0
    else
        return 1
    fi
}

# 检查健康端点
check_health_endpoint() {
    local http_code=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:$PORT/health)
    if [ "$http_code" == "200" ]; then
        return 0
    else
        return 1
    fi
}

# 检查 Docker Registry API
check_registry_api() {
    local http_code=$(curl -s -o /dev/null -w "%{http_code}" http://localhost:$PORT/v2/)
    if [ "$http_code" == "200" ] || [ "$http_code" == "401" ]; then
        return 0  # 200或401都是正常的(401表示需要认证)
    else
        return 1
    fi
}

# 获取响应时间
get_response_time() {
    local url="$1"
    local response_time=$(curl -s -o /dev/null -w "%{time_total}" "$url")
    echo "$response_time"
}

# 获取容器统计信息
get_container_stats() {
    docker stats go-docker-proxy --no-stream --format "table {{.Container}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.NetIO}}\t{{.BlockIO}}"
}

# 获取缓存大小
get_cache_size() {
    if [ -d "./cache" ]; then
        du -sh ./cache 2>/dev/null | awk '{print $1}'
    else
        echo "N/A"
    fi
}

# 获取缓存文件数量
get_cache_file_count() {
    if [ -d "./cache" ]; then
        find ./cache -type f 2>/dev/null | wc -l
    else
        echo "0"
    fi
}

# 检查磁盘空间
check_disk_space() {
    local usage=$(df -h . | awk 'NR==2 {print $5}' | sed 's/%//')
    if [ "$usage" -gt 90 ]; then
        log_error "磁盘空间不足: ${usage}% 已使用"
        return 1
    elif [ "$usage" -gt 80 ]; then
        log_warn "磁盘空间警告: ${usage}% 已使用"
    fi
    return 0
}

# 检查内存使用
check_memory_usage() {
    local mem_usage=$(docker stats go-docker-proxy --no-stream --format "{{.MemPerc}}" | sed 's/%//')
    if (( $(echo "$mem_usage > 90" | bc -l) )); then
        log_error "内存使用过高: ${mem_usage}%"
        return 1
    elif (( $(echo "$mem_usage > 80" | bc -l) )); then
        log_warn "内存使用警告: ${mem_usage}%"
    fi
    return 0
}

# 显示实时状态
show_status() {
    clear
    echo "════════════════════════════════════════════════════════════════"
    echo -e "${GREEN}Docker Registry 代理服务 - 实时监控${NC}"
    echo "════════════════════════════════════════════════════════════════"
    echo ""
    
    # 服务状态
    echo -e "${BLUE}📊 服务状态${NC}"
    echo "────────────────────────────────────────────────────────────────"
    
    if check_service_running; then
        echo -e "容器状态: ${GREEN}✓ 运行中${NC}"
    else
        echo -e "容器状态: ${RED}✗ 未运行${NC}"
    fi
    
    if check_port_listening; then
        echo -e "端口监听: ${GREEN}✓ $PORT${NC}"
    else
        echo -e "端口监听: ${RED}✗ $PORT 未监听${NC}"
    fi
    
    if check_health_endpoint; then
        local health_time=$(get_response_time "http://localhost:$PORT/health")
        echo -e "健康检查: ${GREEN}✓ 正常 (${health_time}s)${NC}"
    else
        echo -e "健康检查: ${RED}✗ 失败${NC}"
    fi
    
    if check_registry_api; then
        local api_time=$(get_response_time "http://localhost:$PORT/v2/")
        echo -e "Registry API: ${GREEN}✓ 正常 (${api_time}s)${NC}"
    else
        echo -e "Registry API: ${RED}✗ 失败${NC}"
    fi
    
    echo ""
    
    # 资源使用
    echo -e "${BLUE}💻 资源使用${NC}"
    echo "────────────────────────────────────────────────────────────────"
    get_container_stats
    echo ""
    
    # 缓存信息
    echo -e "${BLUE}💾 缓存信息${NC}"
    echo "────────────────────────────────────────────────────────────────"
    echo "缓存大小: $(get_cache_size)"
    echo "缓存文件: $(get_cache_file_count) 个"
    echo ""
    
    # 磁盘空间
    local disk_usage=$(df -h . | awk 'NR==2 {print $5}')
    echo -e "${BLUE}💿 磁盘空间${NC}"
    echo "────────────────────────────────────────────────────────────────"
    echo "使用情况: $disk_usage"
    df -h . | awk 'NR==2 {printf "可用空间: %s / %s\n", $4, $2}'
    echo ""
    
    # 最近日志
    echo -e "${BLUE}📝 最近日志 (最后5条)${NC}"
    echo "────────────────────────────────────────────────────────────────"
    docker logs go-docker-proxy --tail 5 2>&1 | tail -5
    echo ""
    
    echo "════════════════════════════════════════════════════════════════"
    echo "刷新时间: $(date +'%Y-%m-%d %H:%M:%S') | 刷新间隔: ${CHECK_INTERVAL}秒"
    echo "按 Ctrl+C 退出监控"
    echo "════════════════════════════════════════════════════════════════"
}

# 持续监控模式
continuous_monitor() {
    log_info "启动持续监控模式 (刷新间隔: ${CHECK_INTERVAL}秒)"
    
    while true; do
        show_status
        sleep $CHECK_INTERVAL
    done
}

# 单次检查模式
single_check() {
    local all_ok=true
    
    echo "执行健康检查..."
    echo ""
    
    # 检查服务
    if check_service_running; then
        log_info "✓ 容器运行正常"
    else
        log_error "✗ 容器未运行"
        all_ok=false
    fi
    
    # 检查端口
    if check_port_listening; then
        log_info "✓ 端口 $PORT 监听正常"
    else
        log_error "✗ 端口 $PORT 未监听"
        all_ok=false
    fi
    
    # 检查健康端点
    if check_health_endpoint; then
        log_info "✓ 健康检查端点正常"
    else
        log_error "✗ 健康检查端点异常"
        all_ok=false
    fi
    
    # 检查 Registry API
    if check_registry_api; then
        log_info "✓ Registry API 正常"
    else
        log_error "✗ Registry API 异常"
        all_ok=false
    fi
    
    # 检查磁盘空间
    check_disk_space
    
    # 检查内存使用
    if check_service_running; then
        check_memory_usage
    fi
    
    echo ""
    
    if [ "$all_ok" = true ]; then
        log_info "所有检查通过 ✓"
        return 0
    else
        log_error "部分检查失败 ✗"
        return 1
    fi
}

# 性能测试
performance_test() {
    log_info "开始性能测试..."
    echo ""
    
    # 测试健康端点
    echo "测试健康端点 (/health)..."
    for i in {1..10}; do
        local time=$(get_response_time "http://localhost:$PORT/health")
        echo "  请求 $i: ${time}s"
    done
    echo ""
    
    # 测试 Registry API
    echo "测试 Registry API (/v2/)..."
    for i in {1..10}; do
        local time=$(get_response_time "http://localhost:$PORT/v2/")
        echo "  请求 $i: ${time}s"
    done
    echo ""
    
    log_info "性能测试完成"
}

# 清理缓存
clean_cache() {
    log_warn "准备清理缓存..."
    
    if [ -d "./cache" ]; then
        local cache_size=$(du -sh ./cache | awk '{print $1}')
        read -p "确认删除 ${cache_size} 的缓存数据? [y/N]: " confirm
        
        if [[ "$confirm" =~ ^[Yy]$ ]]; then
            log_info "正在清理缓存..."
            rm -rf ./cache/*
            log_info "缓存已清理"
        else
            log_info "取消清理操作"
        fi
    else
        log_warn "缓存目录不存在"
    fi
}

# 显示日志
show_logs() {
    local lines="${1:-50}"
    
    log_info "显示最近 $lines 条日志..."
    echo ""
    
    docker logs go-docker-proxy --tail $lines -f
}

# 显示帮助
show_help() {
    cat << EOF
Docker Registry 代理服务 - 监控工具

用法: $0 [选项]

选项:
    -m, --monitor       持续监控模式(默认,每${CHECK_INTERVAL}秒刷新)
    -c, --check         单次健康检查
    -p, --performance   性能测试
    -l, --logs [N]      显示最近N条日志(默认50)
    -s, --stats         显示容器统计信息
    -C, --clean         清理缓存
    -h, --help          显示此帮助信息

环境变量:
    DOMAIN              域名(默认: localhost)
    PORT                端口(默认: 8080)

示例:
    $0 -m               # 持续监控
    $0 -c               # 单次检查
    $0 -p               # 性能测试
    $0 -l 100           # 显示最近100条日志
    $0 -C               # 清理缓存

EOF
}

# 主函数
main() {
    case "${1:-}" in
        -m|--monitor)
            continuous_monitor
            ;;
        -c|--check)
            single_check
            ;;
        -p|--performance)
            performance_test
            ;;
        -l|--logs)
            show_logs "${2:-50}"
            ;;
        -s|--stats)
            get_container_stats
            ;;
        -C|--clean)
            clean_cache
            ;;
        -h|--help)
            show_help
            ;;
        *)
            continuous_monitor
            ;;
    esac
}

# 检查依赖
check_dependencies() {
    local missing_deps=()
    
    for cmd in docker curl nc; do
        if ! command -v $cmd &> /dev/null; then
            missing_deps+=($cmd)
        fi
    done
    
    if [ ${#missing_deps[@]} -gt 0 ]; then
        log_error "缺少依赖: ${missing_deps[*]}"
        log_error "请安装: apt-get install ${missing_deps[*]} (Ubuntu/Debian)"
        log_error "或者: yum install ${missing_deps[*]} (CentOS/RHEL)"
        exit 1
    fi
}

# 程序入口
check_dependencies
main "$@"
