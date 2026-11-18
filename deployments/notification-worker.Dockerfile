# 独立通知 Worker：宿主机预编译的 linux 二进制 + alpine 运行镜像（离线可构建）
# 基础镜像固定精确版本，CI 有 digest 门禁防漂移。
FROM docker.m.daocloud.io/library/alpine:3.20.3

COPY notification-worker-linux-amd64 /usr/local/bin/notification-worker

ENTRYPOINT ["/usr/local/bin/notification-worker"]
