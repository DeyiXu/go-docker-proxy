#!/bin/bash

# 测试 AWS ECR 重定向修复脚本
# 用于验证 Docker Hub 到 AWS S3 的重定向是否正确处理

set -e

PROXY_HOST="${1:-registry.w4w.cc:8080}"
TEST_IMAGE="nginx:latest"

echo "======================================"
echo "测试 Docker 镜像拉取 (AWS S3 重定向)"
echo "======================================"
echo "代理服务器: $PROXY_HOST"
echo "测试镜像: $TEST_IMAGE"
echo ""

# 清理旧的测试镜像
echo "[1/4] 清理旧镜像..."
docker rmi "$PROXY_HOST/$TEST_IMAGE" 2>/dev/null || true
echo "✓ 清理完成"
echo ""

# 测试 manifest 请求
echo "[2/4] 测试 Manifest 请求..."
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" \
    -H "Accept: application/vnd.docker.distribution.manifest.v2+json" \
    "http://$PROXY_HOST/v2/nginx/manifests/latest")

if [ "$HTTP_CODE" = "200" ]; then
    echo "✓ Manifest 请求成功 (HTTP $HTTP_CODE)"
else
    echo "✗ Manifest 请求失败 (HTTP $HTTP_CODE)"
    exit 1
fi
echo ""

# 测试 blob 重定向
echo "[3/4] 测试 Blob 重定向..."
BLOB_SHA="sha256:0f2e0c6f244107fad3fce4b8262ccf2c5c8dbe3f0c89b5974f4d0ee35c1c3bbf"
RESPONSE=$(curl -sI "http://$PROXY_HOST/v2/nginx/blobs/$BLOB_SHA")

if echo "$RESPONSE" | grep -q "HTTP/1.1 30[1-8]"; then
    echo "✓ 收到重定向响应"
    
    LOCATION=$(echo "$RESPONSE" | grep -i "Location:" | cut -d' ' -f2 | tr -d '\r')
    if echo "$LOCATION" | grep -q "amazonaws.com"; then
        echo "✓ 重定向到 AWS S3: $LOCATION"
    else
        echo "⚠ 重定向到: $LOCATION"
    fi
else
    echo "✗ 未收到预期的重定向响应"
    echo "$RESPONSE"
fi
echo ""

# 完整拉取测试
echo "[4/4] 完整拉取测试..."
if timeout 120 docker pull "$PROXY_HOST/$TEST_IMAGE"; then
    echo ""
    echo "✓✓✓ 测试成功! 镜像拉取完成 ✓✓✓"
    
    # 显示镜像信息
    docker images "$PROXY_HOST/$TEST_IMAGE"
    
    # 清理测试镜像
    docker rmi "$PROXY_HOST/$TEST_IMAGE"
else
    echo ""
    echo "✗✗✗ 测试失败! 镜像拉取失败 ✗✗✗"
    exit 1
fi

echo ""
echo "======================================"
echo "所有测试通过!"
echo "======================================"
