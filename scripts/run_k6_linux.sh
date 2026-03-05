#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_PATH="${K6_SCRIPT_PATH:-$ROOT_DIR/scripts/k6_download.js}"
TARGET_URL="${TARGET_URL:-http://load.local/payload_100mb.bin}"
METRICS_URL="${METRICS_URL:-http://127.0.0.1:9101/metrics}"
PROXY_URL="${PROXY_URL:-socks5h://127.0.0.1:1080}"
OUT_DIR="${OUT_DIR:-$ROOT_DIR/k6_runs/$(date +%Y%m%d_%H%M%S)}"
VUS_LIST="${VUS_LIST:-50 500 2000}"

# Optional per-profile durations.
DURATION_DEFAULT="${DURATION_DEFAULT:-20s}"
DURATION_50="${DURATION_50:-$DURATION_DEFAULT}"
DURATION_500="${DURATION_500:-$DURATION_DEFAULT}"
DURATION_2000="${DURATION_2000:-$DURATION_DEFAULT}"

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

duration_for_vus() {
  case "$1" in
    50) echo "$DURATION_50" ;;
    500) echo "$DURATION_500" ;;
    2000) echo "$DURATION_2000" ;;
    *) echo "$DURATION_DEFAULT" ;;
  esac
}

require_cmd curl
require_cmd k6

if [[ ! -f "$SCRIPT_PATH" ]]; then
  echo "k6 script not found: $SCRIPT_PATH" >&2
  exit 1
fi

mkdir -p "$OUT_DIR"

echo "=== k6 linux run ==="
echo "script:  $SCRIPT_PATH"
echo "target:  $TARGET_URL"
echo "proxy:   $PROXY_URL"
echo "metrics: $METRICS_URL"
echo "out:     $OUT_DIR"
echo

export HTTP_PROXY="$PROXY_URL"
export HTTPS_PROXY="$PROXY_URL"
export NO_PROXY="localhost,127.0.0.1"

for vus in $VUS_LIST; do
  dur="$(duration_for_vus "$vus")"
  before="$OUT_DIR/metrics_k6_${vus}_before.txt"
  after="$OUT_DIR/metrics_k6_${vus}_after.txt"
  log="$OUT_DIR/k6_${vus}.log"

  echo "--- profile: vus=$vus duration=$dur ---"
  curl -fsS "$METRICS_URL" > "$before"

  TARGET_URL="$TARGET_URL" k6 run \
    --vus "$vus" \
    --duration "$dur" \
    "$SCRIPT_PATH" | tee "$log"

  curl -fsS "$METRICS_URL" > "$after"
  echo "saved: $log"
  echo "saved: $before"
  echo "saved: $after"
  echo
done

echo "done. artifacts in: $OUT_DIR"
