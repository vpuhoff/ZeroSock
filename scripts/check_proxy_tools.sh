#!/usr/bin/env bash
set -euo pipefail

PROXY_URL="${PROXY_URL:-socks5h://127.0.0.1:1080}"
DOCKER_PROXY_URL="${DOCKER_PROXY_URL:-socks5h://host.docker.internal:1080}"
GIT_TEST_REPO="${GIT_TEST_REPO:-https://github.com/jquery/jquery.git}"
SKOPEO_TEST_IMAGE="${SKOPEO_TEST_IMAGE:-docker://quay.io/libpod/alpine}"

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

  if check_cmd skopeo; then
    HTTPS_PROXY="${PROXY_URL}" HTTP_PROXY="${PROXY_URL}" \
      skopeo inspect "${SKOPEO_TEST_IMAGE}" >/tmp/skopeo_proxy_check.out 2>&1 \
      || fail "skopeo proxy check failed. See /tmp/skopeo_proxy_check.out"
  elif check_cmd docker; then
    warn "skopeo not found locally, using docker image quay.io/skopeo/stable"
    log "Docker skopeo proxy URL: ${DOCKER_PROXY_URL}"
    docker run --rm \
      -e "HTTPS_PROXY=${DOCKER_PROXY_URL}" \
      -e "HTTP_PROXY=${DOCKER_PROXY_URL}" \
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

main() {
  log "Starting proxy checks"
  check_git_via_proxy
  check_skopeo_via_proxy
  log "All checks passed"
}

main "$@"
