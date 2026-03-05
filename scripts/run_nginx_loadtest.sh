#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PAYLOAD_DIR="$ROOT_DIR/loadtest"
NGINX_CONF="$PAYLOAD_DIR/nginx.conf"

if [[ ! -f "$PAYLOAD_DIR/payload_100mb.bin" ]]; then
  echo "payload file not found: $PAYLOAD_DIR/payload_100mb.bin" >&2
  exit 1
fi

if [[ ! -f "$NGINX_CONF" ]]; then
  echo "nginx config not found: $NGINX_CONF" >&2
  exit 1
fi

docker rm -f zerosock-nginx >/dev/null 2>&1 || true
docker run -d --name zerosock-nginx -p 8088:80 \
  -v "$PAYLOAD_DIR:/usr/share/nginx/html:ro" \
  -v "$NGINX_CONF:/etc/nginx/nginx.conf:ro" \
  nginx:alpine >/dev/null

echo "zerosock-nginx started with $NGINX_CONF"
