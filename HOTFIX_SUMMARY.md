# 🚀 AWS S3 重定向修复 - 快速摘要

## 问题
Docker Hub 镜像拉取失败:
```
❌ Error: Missing x-amz-content-sha256
```

## 原因
Docker Hub 的 blob 存储在 AWS S3,代理服务器尝试跟随重定向但缺少 AWS 签名头。

## 解决方案
**将外部存储的重定向直接返回给客户端**,让客户端直接从 S3/CDN 下载。

## 快速更新

```bash
# 拉取代码
git pull origin main

# 重新编译
go build -o go-docker-proxy

# 重启服务(根据你的部署方式选择)
docker-compose restart  # 或
sudo systemctl restart go-docker-proxy
```

## 快速测试

```bash
# 测试拉取
docker pull registry.w4w.cc:8080/nginx:latest

# 或运行测试脚本
./test-aws-redirect.sh registry.w4w.cc:8080
```

## 结果
✅ 支持所有 Docker Hub 镜像
✅ 自动检测外部存储 (AWS, Google, Azure)
✅ 提升下载速度(直接从 CDN)
✅ 降低代理服务器负载

## 详细文档
📖 [AWS_REDIRECT_FIX.md](./AWS_REDIRECT_FIX.md)
📖 [UPGRADE_v1.1.0.md](./UPGRADE_v1.1.0.md)

---
**版本**: v1.1.0  
**状态**: ✅ 已修复  
**影响**: 所有 Docker Hub 镜像拉取
