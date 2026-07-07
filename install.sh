#!/usr/bin/env bash
set -euo pipefail

# ============================================================
# Server Panel one-click installer
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/naibabiji/server-panel/master/install.sh | bash
#
# Optional env:
#   REPO=naibabiji/server-panel
#   VERSION=v1.0.0          # default: latest
#   TLS_PORT=8444           # default: 8444
#   INSTALL_DIR=/www/server/server-panel
#   INSTALL_MODE=upgrade    # existing install only: upgrade, reinstall, exit
# ============================================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

REPO="${REPO:-naibabiji/server-panel}"
VERSION="${VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-/www/server/server-panel}"
TLS_PORT="${TLS_PORT:-8444}"
CONFIG_FILE="$INSTALL_DIR/config.json"
DB_PATH="$INSTALL_DIR/server-panel.db"
BIN_PATH="/usr/local/bin/server-panel"
SERVICE_PATH="/etc/systemd/system/server-panel.service"

log()  { echo -e "${GREEN}[+]${NC} $1"; }
warn() { echo -e "${YELLOW}[!]${NC} $1"; }
err()  { echo -e "${RED}[x]${NC} $1"; exit 1; }

prompt() {
    local __var="$1"
    local message="$2"
    local default="${3:-}"
    local value=""

    if [ -t 0 ]; then
        read -r -p "$message" value || value="$default"
    else
        value="$default"
        echo "${message}${value}"
    fi

    if [ -z "$value" ] && [ -n "$default" ]; then
        value="$default"
    fi
    printf -v "$__var" '%s' "$value"
}

require_root() {
    if [ "$(id -u)" != "0" ]; then
        err "请使用 root 用户运行此脚本"
    fi
}

install_dependencies() {
    log "检查系统依赖..."
    if command -v apt-get >/dev/null 2>&1; then
        export DEBIAN_FRONTEND=noninteractive
        apt-get update -y
        apt-get install -y ca-certificates curl openssl
    elif command -v dnf >/dev/null 2>&1; then
        dnf install -y ca-certificates curl openssl
    elif command -v yum >/dev/null 2>&1; then
        yum install -y ca-certificates curl openssl
    else
        for bin in curl openssl; do
            command -v "$bin" >/dev/null 2>&1 || err "缺少依赖: $bin，请先安装 curl 和 openssl"
        done
    fi
    command -v systemctl >/dev/null 2>&1 || err "当前系统缺少 systemd/systemctl，暂不支持一键安装"
}

detect_arch() {
    case "$(uname -m)" in
        x86_64|amd64) echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *) err "暂不支持当前架构: $(uname -m)" ;;
    esac
}

release_url() {
    local arch="$1"
    local asset="server-panel-linux-${arch}"
    if [ "$VERSION" = "latest" ]; then
        echo "https://github.com/${REPO}/releases/latest/download/${asset}"
    else
        echo "https://github.com/${REPO}/releases/download/${VERSION}/${asset}"
    fi
}

download_file() {
    local url="$1"
    local dest="$2"
    local timeout="${3:-60}"
    curl -fL --retry 3 --connect-timeout 15 --max-time "$timeout" "$url" -o "$dest"
}

install_downloaded_binary() {
    local url="$1"
    local label="$2"
    local tmp_bin
    tmp_bin="$(mktemp)"

    log "尝试下载 Server Panel 二进制: ${label}"
    if download_file "$url" "$tmp_bin" 120; then
        install -m 0755 "$tmp_bin" "$BIN_PATH"
        rm -f "$tmp_bin"
        log "面板二进制下载完成: ${label}"
        return 0
    fi
    rm -f "$tmp_bin"
    warn "${label} 下载失败"
    return 1
}

download_binary() {
    local arch asset url script_dir
    arch="$(detect_arch)"
    asset="server-panel-linux-${arch}"
    url="$(release_url "$arch")"
    script_dir="$(cd "$(dirname "$0")" && pwd)"

    if [ -s "${script_dir}/server-panel" ]; then
        log "使用脚本同目录的 server-panel 二进制"
        install -m 0755 "${script_dir}/server-panel" "$BIN_PATH"
        return
    fi
    if [ -s "${script_dir}/${asset}" ]; then
        log "使用脚本同目录的 ${asset} 二进制"
        install -m 0755 "${script_dir}/${asset}" "$BIN_PATH"
        return
    fi

    if ! install_downloaded_binary "$url" "GitHub Releases 直连"; then
        err "无法获取正式版二进制。解决方案：
  1. 检查服务器能否访问 GitHub Releases
  2. 手动下载 release 附件 ${asset} 后，和 install.sh 放在同一目录重新运行
  3. 或在本机编译后上传为 server-panel，与 install.sh 放在同一目录重新运行"
    fi
}

generate_password() {
    openssl rand -base64 32 | tr -dc 'A-Za-z0-9' | head -c 20
}

hash_password() {
    local password="$1"
    "$BIN_PATH" --hash-password "$password"
}

write_service() {
    log "写入 systemd 服务..."
    cat > "$SERVICE_PATH" <<EOF
[Unit]
Description=Server Panel - Central Server Management Panel
After=network.target

[Service]
Type=simple
User=root
Group=root
ExecStart=$BIN_PATH --config=$CONFIG_FILE
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=server-panel
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload
    systemctl enable server-panel >/dev/null
}

restart_service() {
    log "启动服务..."
    systemctl stop server-panel 2>/dev/null || true
    systemctl start server-panel
    sleep 2
    if systemctl is-active --quiet server-panel; then
        log "服务已启动"
    else
        warn "服务可能未正常启动，请检查: journalctl -u server-panel -n 50"
    fi
}

upgrade_existing() {
    download_binary
    write_service
    restart_service
    echo ""
    log "升级完成，配置和数据库已保留: $CONFIG_FILE"
    echo "  systemctl status server-panel"
    echo "  journalctl -u server-panel -f"
}

remove_service_only() {
    systemctl stop server-panel 2>/dev/null || true
    systemctl disable server-panel 2>/dev/null || true
    rm -f "$SERVICE_PATH"
    systemctl daemon-reload
}

handle_existing_install() {
    local mode

    if [ ! -f "$CONFIG_FILE" ]; then
        return
    fi

    echo ""
    warn "检测到已有安装: $CONFIG_FILE"
    mode="${INSTALL_MODE:-}"
    case "$mode" in
        ""|upgrade)
            upgrade_existing
            exit 0
            ;;
        reinstall)
            remove_service_only
            return
            ;;
        exit)
            exit 0
            ;;
        *)
            err "INSTALL_MODE 只能是 upgrade、reinstall 或 exit"
            ;;
    esac
}

generate_tls_cert() {
    local cert_file key_file cert_san cert_cn
    cert_file="$INSTALL_DIR/certs/panel.crt"
    key_file="$INSTALL_DIR/certs/panel.key"

    if [ -n "$DOMAIN" ]; then
        cert_san="DNS:$DOMAIN,IP:127.0.0.1"
        cert_cn="$DOMAIN"
    else
        cert_san="IP:127.0.0.1"
        cert_cn="Server-Panel-SelfSigned"
    fi

    log "生成自签 TLS 证书..."
    openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
        -keyout "$key_file" \
        -out "$cert_file" \
        -subj "/CN=$cert_cn" \
        -addext "subjectAltName=$cert_san" \
        2>/dev/null
    chmod 600 "$key_file"
    chmod 644 "$cert_file"
}

write_config() {
    local cert_file key_file web_hash basic_hash random_suffix
    cert_file="$INSTALL_DIR/certs/panel.crt"
    key_file="$INSTALL_DIR/certs/panel.key"
    random_suffix="$(openssl rand -base64 24 | tr -dc 'A-Za-z0-9' | head -c 16)"

    WEB_PASSWORD="$(generate_password)"
    BASIC_PASSWORD="$(generate_password)"
    web_hash="$(hash_password "$WEB_PASSWORD")"
    basic_hash="$(hash_password "$BASIC_PASSWORD")"

    log "写入配置文件..."
    cat > "$CONFIG_FILE" <<EOF
{
  "panel": {
    "version": "$VERSION",
    "tls_port": $TLS_PORT,
    "tls_cert_path": "$cert_file",
    "tls_key_path": "$key_file",
    "tls_mode": "self_signed",
    "domain": "$DOMAIN",
    "public_url": "$PUBLIC_URL",
    "acme_email": "",
    "acme_directory_url": "",
    "acme_storage_path": "",
    "acme_challenge_port": 80,
    "random_suffix": "$random_suffix",
    "data_dir": "$INSTALL_DIR",
    "log_dir": "$INSTALL_DIR/logs",
    "panel_title": "Server Panel"
  },
  "sqlite": {
    "path": "$DB_PATH"
  },
  "admin": {
    "username": "spadmin",
    "password_hash": "$web_hash"
  },
  "basic_auth": {
    "username": "admin",
    "password_hash": "$basic_hash"
  },
  "security": {
    "basic_auth_enabled": true,
    "max_login_attempts": 5,
    "attempt_window_minutes": 5,
    "ban_duration_hours": 24
  },
  "systemd": {
    "service_name": "server-panel",
    "service_path": "$SERVICE_PATH",
    "binary_path": "$BIN_PATH"
  }
}
EOF
    chmod 600 "$CONFIG_FILE"
    RANDOM_SUFFIX="$random_suffix"
}

print_summary() {
    local server_ip panel_url
    if [ -n "$DOMAIN" ]; then
        panel_url="https://${DOMAIN}:${TLS_PORT}/${RANDOM_SUFFIX}/login"
    else
        server_ip="$(hostname -I 2>/dev/null | awk '{print $1}')"
        panel_url="https://${server_ip:-YOUR_SERVER_IP}:${TLS_PORT}/${RANDOM_SUFFIX}/login"
    fi

    echo ""
    echo "============================================"
    echo "  Server Panel 安装完成"
    echo "============================================"
    echo ""
    echo -e "  ${CYAN}面板地址:${NC} $panel_url"
    echo -e "  ${CYAN}BasicAuth:${NC} admin / ${GREEN}${BASIC_PASSWORD}${NC}"
    echo -e "  ${CYAN}面板登录:${NC} spadmin / ${GREEN}${WEB_PASSWORD}${NC}"
    echo -e "  ${CYAN}安装目录:${NC} $INSTALL_DIR"
    echo -e "  ${CYAN}配置文件:${NC} $CONFIG_FILE"
    echo ""
    echo -e "  ${YELLOW}请立即保存以上地址和密码。自签证书会有浏览器安全提示，公网使用建议放在 Nginx/Caddy 反向代理后。${NC}"
    echo ""
    echo "  管理命令:"
    echo "    systemctl status server-panel"
    echo "    systemctl restart server-panel"
    echo "    journalctl -u server-panel -f"
    echo "    server-panel --reset-password"
    echo ""
}

main() {
    require_root
    install_dependencies
    handle_existing_install

    mkdir -p "$INSTALL_DIR/certs" "$INSTALL_DIR/logs"

    echo ""
    echo "============================================"
    echo "  Server Panel TLS 配置"
    echo "============================================"
    DOMAIN="${DOMAIN:-}"
    PUBLIC_URL="${PUBLIC_URL:-}"
    if [ -n "$DOMAIN" ]; then
        PUBLIC_URL="${PUBLIC_URL:-https://${DOMAIN}:${TLS_PORT}}"
        log "使用环境变量中的面板域名: $DOMAIN"
    else
        prompt bind_domain "是否绑定面板域名? [y/N]: " "n"
    fi
    if [ -z "$DOMAIN" ] && [[ "${bind_domain:-}" =~ ^[Yy] ]]; then
        prompt DOMAIN "面板域名（如 panel.example.com）: "
        if [ -n "$DOMAIN" ]; then
            prompt tls_port_input "HTTPS 端口 (默认 ${TLS_PORT}): "
            TLS_PORT="${tls_port_input:-$TLS_PORT}"
            PUBLIC_URL="https://${DOMAIN}:${TLS_PORT}"
        fi
    fi

    download_binary
    generate_tls_cert
    write_config
    write_service
    restart_service
    print_summary
}

main "$@"
