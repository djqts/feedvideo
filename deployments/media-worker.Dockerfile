# 媒体处理 Worker：基于 ffmpeg 镜像运行宿主机预编译的 Go 二进制（离线可构建）
# ffmpeg 基础镜像固定精确版本标签（linuxserver 提供版本化标签）。
FROM linuxserver/ffmpeg:7.1.1

COPY media-worker-linux-amd64 /usr/local/bin/media-worker

ENTRYPOINT ["/usr/local/bin/media-worker"]
