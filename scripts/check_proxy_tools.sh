#!/usr/bin/env bash
set -euo pipefail

PROXY_URL="${PROXY_URL:-socks5h://127.0.0.1:1080}"
DOCKER_PROXY_URL="${DOCKER_PROXY_URL:-socks5h://host.docker.internal:1080}"
GIT_TEST_REPO="${GIT_TEST_REPO:-https://github.com/jquery/jquery.git}"
SKOPEO_TEST_IMAGE="${SKOPEO_TEST_IMAGE:-docker://quay.io/libpod/alpine}"
# IP for curl check: must be in config routes (backend whitelist). Default: GitHub IP (e.g. routes "github.com").
CURL_IP_TEST_URL="${CURL_IP_TEST_URL:-https://140.82.121.4/}"

log() {
  printf '[check] %s\n' "$*"
}

warn() {
  printf '[warn] %s\n' "$*" >&2
}

fail() {
  printf '[fail] %s\n' "$*" >&2
  exit 1
}

check_cmd() {
  command -v "$1" >/dev/null 2>&1
}

check_git_via_proxy() {
  check_cmd git || fail "git is not installed"
  log "Testing git via proxy: ${PROXY_URL}"
  git -c "http.proxy=${PROXY_URL}" ls-remote "${GIT_TEST_REPO}" HEAD >/tmp/git_proxy_check.out 2>&1 \
    || fail "git proxy check failed. See /tmp/git_proxy_check.out"

  local head_line
  head_line="$(awk 'NR==1 {print; exit}' /tmp/git_proxy_check.out)"
  log "git OK: ${head_line:-HEAD resolved}"
}

check_skopeo_via_proxy() {
  log "Testing skopeo via proxy: ${PROXY_URL}"

  # Go's net/http (used by skopeo) does not support socks5h:// and misparses it as host "socks5h".
  # Use socks5:// for skopeo; ZeroSock resolves client's IP destination via backend whitelist (IPv4 routing).
  local skopeo_proxy="${PROXY_URL}"
  if [[ "$skopeo_proxy" == socks5h://* ]]; then
    skopeo_proxy="socks5://${skopeo_proxy#socks5h://}"
  fi

  if check_cmd skopeo; then
    HTTPS_PROXY="${skopeo_proxy}" HTTP_PROXY="${skopeo_proxy}" \
      skopeo inspect "${SKOPEO_TEST_IMAGE}" >/tmp/skopeo_proxy_check.out 2>&1 \
      || fail "skopeo proxy check failed. See /tmp/skopeo_proxy_check.out"
  elif check_cmd docker; then
    warn "skopeo not found locally, using docker image quay.io/skopeo/stable"
    local docker_proxy="${DOCKER_PROXY_URL}"
    if [[ "$docker_proxy" == socks5h://* ]]; then
      docker_proxy="socks5://${docker_proxy#socks5h://}"
    fi
    log "Docker skopeo proxy URL: ${docker_proxy}"
    docker run --rm \
      -e "HTTPS_PROXY=${docker_proxy}" \
      -e "HTTP_PROXY=${docker_proxy}" \
      quay.io/skopeo/stable:latest inspect "${SKOPEO_TEST_IMAGE}" \
      >/tmp/skopeo_proxy_check.out 2>&1 \
      || fail "skopeo(docker) proxy check failed. See /tmp/skopeo_proxy_check.out"
  else
    fail "neither skopeo nor docker found"
  fi

  local digest
  digest="$(awk -F': ' '/"Digest"/ {gsub(/[",]/, "", $2); print $2; exit}' /tmp/skopeo_proxy_check.out)"
  log "skopeo OK: digest=${digest:-unknown}"
}

check_curl_ip_via_proxy() {
  check_cmd curl || fail "curl is not installed"
  log "Testing curl via proxy to IP: ${CURL_IP_TEST_URL}"

  local code
  code=$(curl -x "${PROXY_URL}" -k -I -sS -o /tmp/curl_ip_check.out -w "%{http_code}" --connect-timeout 15 "${CURL_IP_TEST_URL}" 2>/tmp/curl_ip_check.err) || true
  if [[ "$code" != "2"* && "$code" != "3"* ]]; then
    cat /tmp/curl_ip_check.out /tmp/curl_ip_check.err >&2
    fail "curl IP check failed (HTTP ${code}). See /tmp/curl_ip_check.out and /tmp/curl_ip_check.err"
  fi
  log "curl OK: HTTP ${code} (request by IP, proxy whitelist)"
}

main() {
  log "Starting proxy checks"
  check_git_via_proxy
  check_curl_ip_via_proxy
  check_skopeo_via_proxy
  log "All checks passed"
}

main "$@"
