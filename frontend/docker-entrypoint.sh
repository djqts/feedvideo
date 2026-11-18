#!/bin/sh
set -e

# FeedVideo nginx 入口（P0.1）
# - 首次启动且无证书时生成自签证书（本地/离线环境）
# - 生产环境请将真实证书（fullchain.pem / privkey.pem）挂载到
#   /etc/nginx/certs，并配置 Let's Encrypt 自动续期与过期告警
#   （详见 deployments/README.md 的“TLS 证书”章节）

CERT_DIR=/etc/nginx/certs
mkdir -p "$CERT_DIR"

if [ ! -s "$CERT_DIR/fullchain.pem" ] || [ ! -s "$CERT_DIR/privkey.pem" ]; then
  echo "No TLS cert found, attempting self-signed generation (dev fallback)..."
  if command -v openssl >/dev/null 2>&1; then
    openssl req -x509 -nodes -days 90 -newkey rsa:2048 \
      -keyout "$CERT_DIR/privkey.pem" \
      -out "$CERT_DIR/fullchain.pem" \
      -subj "/CN=feedvideo.local" >/dev/null 2>&1
    chmod 600 "$CERT_DIR/privkey.pem"
  else
    echo "WARNING: openssl not available in this image and no certs mounted at $CERT_DIR."
    echo "Mount fullchain.pem/privkey.pem (e.g. -v ./certs:$CERT_DIR) or use an image with openssl."
    echo "nginx will fail to start until TLS certs are provided (production mounts real certs)."
  fi
fi

if [ "$NGINX_ENTRYPOINT_QUIET_LOGGING" != "1" ]; then
  echo "TLS certificates ready: $CERT_DIR/fullchain.pem"
fi

exec nginx -g "daemon off;"
