#!/usr/bin/env bash
# 在宿主机交叉编译 Compose 需要的三个 linux/amd64 二进制。
# 保留原脚本名以兼容已有命令；代理和缓存遵循调用者的 Go 配置。
# 用法: bash deployments/build-gateway.sh
set -euo pipefail
cd "$(dirname "$0")/.."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -o deployments/gateway-linux-amd64 ./gateway/cmd/server/
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -o deployments/media-worker-linux-amd64 ./gateway/cmd/media-worker/
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -buildvcs=false -o deployments/notification-worker-linux-amd64 ./notification/cmd/worker/
echo "built gateway, media-worker and notification-worker (linux/amd64)"
