# ---- 运行阶段（单阶段）----
# 说明：离线环境无法拉取 golang:1.25-alpine 且容器内无法下载 Go 模块，
# 因此 gateway 二进制在宿主机交叉编译（见 build-gateway.sh），此处仅打包。
# 使用 daocloud 镜像源的 alpine（本地可用）；busybox 自带 wget 供 healthcheck，无需 apk。
# 基础镜像固定精确版本，CI 有 digest 门禁防漂移。
FROM docker.m.daocloud.io/library/alpine:3.20.3

WORKDIR /app

# 宿主机预编译的静态 linux/amd64 二进制（CGO_ENABLED=0）
COPY deployments/gateway-linux-amd64 /app/gateway

# Startup checks the same manifest used by the explicit migration command.
COPY migrations/ /app/migrations/

# config.yml 通过 compose 卷挂载注入 /app/config/config.yml
# RSA 密钥由 main.go 首次启动时自动生成写入 /app

EXPOSE 8080

CMD ["/app/gateway"]
