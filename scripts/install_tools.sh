#!/usr/bin/env bash
# Установка инструментов для ZeroSock: Go, Docker, k6, skopeo, curl.
# Поддерживается: Debian, Ubuntu. Для других дистрибутивов — только подсказки.
set -euo pipefail

GO_MIN_VERSION="1.21"
GO_INSTALL_VERSION="${GO_VERSION:-1.22.0}"
K6_DEB_KEYRING="/usr/share/keyrings/k6-archive-keyring.gpg"
K6_DEB_LIST="/etc/apt/sources.list.d/k6.list"

usage() {
  echo "Usage: $0 [OPTIONS]"
  echo ""
  echo "Options:"
  echo "  -n, --dry-run    только показать, что будет установлено"
  echo "  -h, --help       эта справка"
  echo ""
  echo "Переменные окружения:"
  echo "  GO_VERSION       версия Go (default: $GO_INSTALL_VERSION)"
  echo "  SKIP_GO          не устанавливать Go"
  echo "  SKIP_DOCKER      не устанавливать Docker"
  echo "  SKIP_K6          не устанавливать k6"
  echo "  SKIP_SKOPEO      не устанавливать skopeo"
}

DRY_RUN=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    -n|--dry-run) DRY_RUN=true; shift ;;
    -h|--help)    usage; exit 0 ;;
    *)            echo "Unknown option: $1" >&2; usage; exit 1 ;;
  esac
done

run() {
  if [[ "$DRY_RUN" == true ]]; then
    echo "[dry-run] $*"
  else
    "$@"
  fi
}

need_sudo() {
  if [[ $EUID -ne 0 ]] && ! command -v sudo >/dev/null 2>&1; then
    echo "Для установки пакетов нужны права root или sudo." >&2
    exit 1
  fi
  if [[ $EUID -ne 0 ]]; then
    SUDO="sudo"
  else
    SUDO=""
  fi
}

# Определение дистрибутива
detect_os() {
  if [[ -f /etc/os-release ]]; then
    # shellcheck source=/dev/null
    source /etc/os-release
    OS_ID="${ID:-unknown}"
    OS_VERSION_ID="${VERSION_ID:-}"
  else
    OS_ID="unknown"
  fi
}

# --- curl ---
install_curl() {
  echo "=== curl ==="
  if command -v curl >/dev/null 2>&1; then
    echo "curl уже установлен: $(curl --version | head -1)"
    return
  fi
  need_sudo
  case "$OS_ID" in
    ubuntu|debian)
      run $SUDO apt-get update -qq
      run $SUDO apt-get install -y curl
      ;;
    fedora|rhel|centos)
      run $SUDO dnf install -y curl 2>/dev/null || run $SUDO yum install -y curl
      ;;
    *)
      echo "Установите curl вручную для вашего дистрибутива." >&2
      exit 1
      ;;
  esac
  echo "curl установлен."
}

# --- Go ---
install_go() {
  echo "=== Go (требуется >= $GO_MIN_VERSION) ==="
  if command -v go >/dev/null 2>&1; then
    ver=$(go version | grep -oE 'go[0-9]+\.[0-9]+' | tr -d 'go')
    major=$(echo "$ver" | cut -d. -f1)
    minor=$(echo "$ver" | cut -d. -f2)
    need_major=$(echo "$GO_MIN_VERSION" | cut -d. -f1)
    need_minor=$(echo "$GO_MIN_VERSION" | cut -d. -f2)
    if [[ "$major" -gt "$need_major" ]] || { [[ "$major" -eq "$need_major" ]] && [[ "$minor" -ge "$need_minor" ]]; }; then
      echo "Go уже установлен: $(go version)"
      return
    fi
    echo "Найден устаревший Go $ver, ставим $GO_INSTALL_VERSION"
  fi

  need_sudo
  arch=$(uname -m)
  case "$arch" in
    x86_64)  GO_ARCH="amd64" ;;
    aarch64|arm64) GO_ARCH="arm64" ;;
    *)       echo "Неподдерживаемая архитектура: $arch" >&2; exit 1 ;;
  esac

  tarball="go${GO_INSTALL_VERSION}.linux-${GO_ARCH}.tar.gz"
  url="https://go.dev/dl/${tarball}"
  tmpdir="${TMPDIR:-/tmp}/zerosock-install-$$"
  mkdir -p "$tmpdir"
  trap 'rm -rf "$tmpdir"' EXIT

  echo "Скачивание $url ..."
  if [[ "$DRY_RUN" == true ]]; then
    echo "[dry-run] curl -fSL $url -o $tmpdir/$tarball"
    echo "[dry-run] tar -C /usr/local -xzf $tmpdir/$tarball"
  else
    curl -fsSL "$url" -o "$tmpdir/$tarball"
    run $SUDO rm -rf /usr/local/go
    run $SUDO tar -C /usr/local -xzf "$tmpdir/$tarball"
  fi

  if [[ "$DRY_RUN" != true ]]; then
    if ! grep -q '/usr/local/go/bin' /etc/environment 2>/dev/null; then
      echo "Добавьте в PATH: export PATH=\$PATH:/usr/local/go/bin"
      echo "Или выполните: export PATH=\$PATH:/usr/local/go/bin"
    fi
    export PATH="$PATH:/usr/local/go/bin"
    if command -v go >/dev/null 2>&1; then
      echo "Go установлен: $(go version)"
    else
      echo "Go установлен в /usr/local/go. Добавьте в PATH: export PATH=\$PATH:/usr/local/go/bin"
    fi
  fi
}

# --- Docker ---
install_docker() {
  echo "=== Docker ==="
  if command -v docker >/dev/null 2>&1 && docker run --rm hello-world >/dev/null 2>&1; then
    echo "Docker уже установлен и работает: $(docker --version)"
    return
  fi

  need_sudo
  case "$OS_ID" in
    ubuntu|debian)
      if ! command -v docker >/dev/null 2>&1; then
        run $SUDO apt-get update -qq
        run $SUDO apt-get install -y ca-certificates curl 2>/dev/null || true
        echo "Установка Docker через get.docker.com ..."
        if [[ "$DRY_RUN" == true ]]; then
          echo "[dry-run] curl -fsSL https://get.docker.com | sh"
        else
          curl -fsSL https://get.docker.com | sh
        fi
        if [[ $EUID -ne 0 ]]; then
          echo "Добавьте пользователя в группу docker: sudo usermod -aG docker \$USER"
          echo "И перелогиньтесь или выполните: newgrp docker"
        fi
      else
        echo "Docker установлен, но возможно не запущен или нет прав. Запустите: sudo systemctl start docker"
      fi
      ;;
    fedora|rhel|centos)
      run $SUDO dnf install -y dnf-plugins-core 2>/dev/null || true
      run $SUDO dnf config-manager --add-repo https://download.docker.com/linux/fedora/docker-ce.repo 2>/dev/null || true
      run $SUDO dnf install -y docker-ce docker-ce-cli containerd.io 2>/dev/null || run $SUDO yum install -y docker docker-ce 2>/dev/null || true
      run $SUDO systemctl enable --now docker 2>/dev/null || true
      ;;
    *)
      echo "Установите Docker вручную: https://docs.docker.com/engine/install/" >&2
      exit 1
      ;;
  esac
  echo "Docker готов к использованию."
}

# --- k6 ---
install_k6() {
  echo "=== k6 ==="
  if command -v k6 >/dev/null 2>&1; then
    echo "k6 уже установлен: $(k6 version 2>/dev/null || k6 version)"
    return
  fi

  need_sudo
  case "$OS_ID" in
    ubuntu|debian)
      echo "Добавление репозитория k6 ..."
      if [[ "$DRY_RUN" == true ]]; then
        echo "[dry-run] gpg, apt repo, apt install k6"
      else
        run $SUDO apt-get install -y gpg 2>/dev/null || true
        run $SUDO gpg -k 2>/dev/null || true
        run $SUDO gpg --no-default-keyring --keyring "$K6_DEB_KEYRING" \
          --keyserver hkp://keyserver.ubuntu.com:80 \
          --recv-keys C5AD17C747E3415A3642D57D77C6C491D6AC1D69 2>/dev/null || true
        echo "deb [signed-by=$K6_DEB_KEYRING] https://dl.k6.io/deb stable main" | run $SUDO tee "$K6_DEB_LIST" >/dev/null
        run $SUDO apt-get update -qq
        run $SUDO apt-get install -y k6
      fi
      ;;
    fedora|rhel|centos)
      if [[ "$DRY_RUN" == true ]]; then
        echo "[dry-run] dnf install k6 repo and k6"
      else
        run $SUDO dnf install -y https://dl.k6.io/rpm/repo.rpm 2>/dev/null || true
        run $SUDO dnf install -y k6 2>/dev/null || run $SUDO yum install -y k6 2>/dev/null || true
      fi
      ;;
    *)
      echo "Установка k6 вручную: скачайте бинарник с https://github.com/grafana/k6/releases" >&2
      echo "Или: go install go.k6.io/k6@latest" >&2
      exit 1
      ;;
  esac
  if command -v k6 >/dev/null 2>&1; then
    echo "k6 установлен: $(k6 version 2>/dev/null || true)"
  fi
}

# --- skopeo ---
install_skopeo() {
  echo "=== skopeo ==="
  if command -v skopeo >/dev/null 2>&1; then
    echo "skopeo уже установлен: $(skopeo --version 2>/dev/null | head -1)"
    return
  fi

  need_sudo
  case "$OS_ID" in
    ubuntu|debian)
      run $SUDO apt-get update -qq
      run $SUDO apt-get install -y skopeo
      ;;
    fedora|rhel|centos)
      run $SUDO dnf install -y skopeo 2>/dev/null || run $SUDO yum install -y skopeo 2>/dev/null
      ;;
    *)
      echo "Установите skopeo вручную для вашего дистрибутива (apt install skopeo / dnf install skopeo)." >&2
      exit 1
      ;;
  esac
  echo "skopeo установлен: $(skopeo --version 2>/dev/null | head -1)"
}

# --- main ---
main() {
  detect_os
  echo "Дистрибутив: $OS_ID"
  echo ""

  install_curl

  [[ "${SKIP_GO:-}" != "1" ]] && install_go || echo "=== Go (пропуск, SKIP_GO=1) ==="
  [[ "${SKIP_DOCKER:-}" != "1" ]] && install_docker || echo "=== Docker (пропуск, SKIP_DOCKER=1) ==="
  [[ "${SKIP_K6:-}" != "1" ]] && install_k6 || echo "=== k6 (пропуск, SKIP_K6=1) ==="
  [[ "${SKIP_SKOPEO:-}" != "1" ]] && install_skopeo || echo "=== skopeo (пропуск, SKIP_SKOPEO=1) ==="

  echo ""
  echo "--- Итог ---"
  echo "Добавьте Go в PATH, если ещё не добавлен:"
  echo "  export PATH=\$PATH:/usr/local/go/bin"
  echo ""
  echo "Проверка:"
  echo "  go version"
  echo "  docker run --rm hello-world"
  echo "  k6 version"
  echo "  skopeo --version"
  echo "  curl -V"
}

main
