#!/usr/bin/env bash
# Полный сценарий: ZeroSock → nginx → проверка → k6.
# Требует: go, docker, k6, curl. Запускайте на машине, где они установлены.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

CONFIG="${CONFIG:-$ROOT_DIR/config.yaml}"
PAYLOAD="$ROOT_DIR/loadtest/payload_100mb.bin"
NGINX_CONF="$ROOT_DIR/loadtest/nginx.conf"

echo "=== 1. Проверка payload ==="
if [[ ! -f "$PAYLOAD" ]]; then
  echo "Создаю loadtest/payload_100mb.bin (100 МБ)..."
  dd if=/dev/zero of="$PAYLOAD" bs=1M count=10 status=progress
fi

echo "=== 2. Запуск nginx (Docker) ==="
docker rm -f zerosock-nginx >/dev/null 2>&1 || true
docker run -d --name zerosock-nginx -p 8088:80 \
  -v "$ROOT_DIR/loadtest:/usr/share/nginx/html:ro" \
  -v "$NGINX_CONF:/etc/nginx/nginx.conf:ro" \
  nginx:alpine >/dev/null
echo "nginx слушает на 127.0.0.1:8088"

echo "=== 3. Запуск ZeroSock (фоновый процесс) ==="
# Убиваем старый процесс на порту 1080, если есть
if command -v fuser >/dev/null 2>&1; then
  fuser -k 1080/tcp 2>/dev/null || true
  sleep 1
fi
go run ./cmd/zerosock -config "$CONFIG" &
ZEROSOCK_PID=$!
trap 'kill $ZEROSOCK_PID 2>/dev/null || true' EXIT
sleep 2
if ! kill -0 $ZEROSOCK_PID 2>/dev/null; then
  echo "Ошибка: ZeroSock не запустился" >&2
  exit 1
fi
echo "ZeroSock PID=$ZEROSOCK_PID, порт 1080"

echo "=== 4. Проверка nginx и прокси ==="
# Прямая проверка nginx
code=$(curl -fsS -o /dev/null -w "%{http_code}" --connect-timeout 3 http://127.0.0.1:8088/payload_100mb.bin 2>/dev/null || echo "000")
if [[ "$code" == "200" ]]; then
  echo "nginx (127.0.0.1:8088): OK"
else
  echo "nginx вернул $code (ожидался 200)"
fi
# Проверка через прокси: socks5h передаёт host в прокси, роут load.local -> 127.0.0.1:8088
export HTTP_PROXY=socks5h://127.0.0.1:1080
export HTTPS_PROXY=socks5h://127.0.0.1:1080
code_proxy=$(curl -fsS -o /dev/null -w "%{http_code}" --connect-timeout 5 http://load.local/payload_100mb.bin 2>/dev/null || echo "000")
if [[ "$code_proxy" == "200" ]]; then
  echo "через прокси (load.local -> 8088): OK"
else
  echo "через прокси: $code_proxy (добавьте 127.0.0.1 load.local в /etc/hosts при необходимости)"
fi

echo "=== 5. Запуск k6 ==="
export TARGET_URL="${TARGET_URL:-http://load.local/payload_100mb.bin}"
export METRICS_URL="${METRICS_URL:-http://127.0.0.1:9101/metrics}"
export PROXY_URL="${PROXY_URL:-socks5h://127.0.0.1:1080}"
# Короткий прогон по умолчанию
export VUS_LIST="${VUS_LIST:-50}"
export DURATION_DEFAULT="${DURATION_DEFAULT:-15s}"
bash "$ROOT_DIR/scripts/run_k6_linux.sh"

echo "=== Готово ==="
