#!/bin/bash
set -e

# ============================================================
# Server Panel 安装脚本 — Debian 13
# ============================================================

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
CYAN='\033[0;36m'
NC='\033[0m'

INSTALL_DIR="/www/server/server-panel"
CONFIG_FILE="$INSTALL_DIR/config.json"
DB_PATH="$INSTALL_DIR/server-panel.db"
BIN_PATH="/usr/local/bin/server-panel"
SERVICE_PATH="/etc/systemd/system/server-panel.service"
PANEL_PORT=8888
TLS_PORT=8443

log()  { echo -e "${GREEN}[+]${NC} $1"; }
warn() { echo -e "${YELLOW}[!]${NC} $1"; }
err()  { echo -e "${RED}[x]${NC} $1"; exit 1; }

# ============================================================
# 1. Root check
# ============================================================
if [ "$(id -u)" != "0" ]; then
    err "请使用 root 用户运行此脚本"
fi

# ============================================================
# 2. Detect existing install
# ============================================================
if [ -f "$CONFIG_FILE" ]; then
    echo ""
    warn "检测到已有安装: $CONFIG_FILE"
    echo "  1) 覆盖安装（保留数据库和证书）"
    echo "  2) 完全卸载后重新安装"
    echo "  3) 退出"
    read -p "请选择 [1-3]: " reinstall_choice
    case $reinstall_choice in
        1) log "覆盖安装模式" ;;
        2)
            log "卸载中..."
            systemctl stop server-panel 2>/dev/null || true
            systemctl disable server-panel 2>/dev/null || true
            rm -f "$SERVICE_PATH"
            systemctl daemon-reload
            log "已卸载，继续安装"
            ;;
        *) exit 0 ;;
    esac
fi

# ============================================================
# 3. Create directories
# ============================================================
log "创建目录..."
mkdir -p "$INSTALL_DIR"/{certs,logs}
mkdir -p "$INSTALL_DIR/acme"

# ============================================================
# 4. Domain / TLS setup
# ============================================================
echo ""
echo "============================================"
echo "  TLS 证书配置"
echo "============================================"
echo ""
echo "  推荐: 绑定域名 + Let's Encrypt 自动签发可信证书"
echo "  备选: 自签证书（IP 直连/内网使用）"
echo ""
read -p "是否绑定面板域名? [y/N]: " bind_domain

TLS_MODE="self_signed"
DOMAIN=""
ACME_EMAIL=""
PUBLIC_URL=""

if [[ "$bind_domain" =~ ^[Yy] ]]; then
    read -p "面板域名（如 panel.example.com）: " DOMAIN
    if [ -z "$DOMAIN" ]; then
        warn "域名不能为空，将使用自签证书"
    else
        read -p "ACME 邮箱（用于 Let's Encrypt 通知）: " ACME_EMAIL
        if [ -z "$ACME_EMAIL" ]; then
            warn "邮箱不能为空，将使用自签证书"
        else
            # Check if port 80 is available for ACME challenge
            echo ""
            log "正在检查 ACME 签发条件..."

            # Check DNS resolution
            SERVER_IP=$(curl -s4 --connect-timeout 5 ifconfig.me 2>/dev/null || echo "")
            if [ -n "$SERVER_IP" ]; then
                DOMAIN_IP=$(dig +short "$DOMAIN" 2>/dev/null || echo "")
                if [ -n "$DOMAIN_IP" ] && [ "$DOMAIN_IP" = "$SERVER_IP" ]; then
                    log "域名解析正常: $DOMAIN -> $DOMAIN_IP"
                else
                    warn "域名 $DOMAIN 未解析到本机 ($SERVER_IP)，ACME 签发可能失败"
                    warn "如需继续，请确保域名已正确解析"
                    read -p "是否仍尝试 ACME? [y/N]: " force_acme
                    if [[ ! "$force_acme" =~ ^[Yy] ]]; then
                        warn "将使用自签证书"
                        DOMAIN=""
                    fi
                fi
            fi

            if [ -n "$DOMAIN" ]; then
                read -p "HTTPS 端口 (默认 8443): " tls_port_input
                TLS_PORT="${tls_port_input:-8443}"

                TLS_MODE="acme_http01"
                PUBLIC_URL="https://${DOMAIN}:${TLS_PORT}"
                log "TLS 模式: ACME HTTP-01"
                log "域名: $DOMAIN"
                log "HTTPS 端口: $TLS_PORT"
            fi
        fi
    fi
fi

# Generate self-signed cert as fallback / initial cert
log "生成 TLS 证书..."
CERT_FILE="$INSTALL_DIR/certs/panel.crt"
KEY_FILE="$INSTALL_DIR/certs/panel.key"

openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
    -keyout "$KEY_FILE" \
    -out "$CERT_FILE" \
    -subj "/CN=Server-Panel-SelfSigned" \
    -addext "subjectAltName=IP:127.0.0.1" \
    2>/dev/null
chmod 600 "$KEY_FILE"
chmod 644 "$CERT_FILE"

if [ "$TLS_MODE" = "acme_http01" ]; then
    log "自签证书已生成作为初始证书，ACME 签发后自动切换"
    log "面板启动后可在 系统设置 中查看证书状态并手动签发/续期"
fi

# ============================================================
# 5. Generate random suffix
# ============================================================
log "生成随机路径后缀..."
RANDOM_SUFFIX=$(head -c 12 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 16)

# ============================================================
# 6. Generate passwords
# ============================================================
log "生成认证密码..."
WEB_PASSWORD=$(head -c 16 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 16)
BASIC_PASSWORD=$(head -c 16 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 16)

# Generate bcrypt hash using Go (if available) or Python
generate_bcrypt() {
    local password="$1"
    if command -v go &>/dev/null; then
        cat > /tmp/bcrypt_gen.go << 'GOSRC'
package main
import ("fmt"; "golang.org/x/crypto/bcrypt"; "os")
func main() {
    hash, _ := bcrypt.GenerateFromPassword([]byte(os.Args[1]), bcrypt.DefaultCost)
    fmt.Print(string(hash))
}
GOSRC
        cd /tmp
        go run bcrypt_gen.go "$password" 2>/dev/null || python3 -c "import bcrypt; print(bcrypt.hashpw('${password}'.encode(), bcrypt.gensalt()).decode())" 2>/dev/null || echo ""
        rm -f /tmp/bcrypt_gen.go
    elif command -v python3 &>/dev/null; then
        python3 -c "import bcrypt; print(bcrypt.hashpw('${password}'.encode(), bcrypt.gensalt()).decode())" 2>/dev/null || echo ""
    else
        echo ""
    fi
}

log "生成 bcrypt 哈希..."
WEB_HASH=$(generate_bcrypt "$WEB_PASSWORD")
BASIC_HASH=$(generate_bcrypt "$BASIC_PASSWORD")

if [ -z "$WEB_HASH" ] || [ -z "$BASIC_HASH" ]; then
    err "无法生成 bcrypt 哈希，请确保已安装 Go 或 Python3 + bcrypt"
fi

# ============================================================
# 7. Generate pepper
# ============================================================
PEPPER=$(head -c 32 /dev/urandom | xxd -p | tr -d '\n')

# ============================================================
# 8. Deploy binary
# ============================================================
if [ -f "./server-panel" ]; then
    log "部署本地二进制文件..."
    cp ./server-panel "$BIN_PATH"
elif [ -f "$BIN_PATH" ]; then
    log "使用现有二进制文件: $BIN_PATH"
else
    warn "未找到 server-panel 二进制文件"
    warn "请将编译好的二进制文件放置到当前目录或 $BIN_PATH"
    warn "例如: go build -ldflags '-s -w' -o server-panel ."
    read -p "是否已手动放置二进制文件? [y/N]: " has_bin
    if [[ ! "$has_bin" =~ ^[Yy] ]]; then
        err "安装中止，请先编译二进制文件"
    fi
    if [ ! -f "$BIN_PATH" ]; then
        err "未找到 $BIN_PATH"
    fi
fi
chmod +x "$BIN_PATH"

# ============================================================
# 9. Write config.json
# ============================================================
log "写入配置文件..."
cat > "$CONFIG_FILE" << EOF
{
  "panel": {
    "version": "1.0.0",
    "port": $PANEL_PORT,
    "tls_port": $TLS_PORT,
    "tls_cert_path": "$CERT_FILE",
    "tls_key_path": "$KEY_FILE",
    "tls_mode": "$TLS_MODE",
    "domain": "$DOMAIN",
    "public_url": "$PUBLIC_URL",
    "acme_email": "$ACME_EMAIL",
    "acme_directory_url": "https://acme-v02.api.letsencrypt.org/directory",
    "acme_storage_path": "$INSTALL_DIR/acme",
    "acme_challenge_port": 80,
    "random_suffix": "$RANDOM_SUFFIX",
    "data_dir": "$INSTALL_DIR",
    "log_dir": "$INSTALL_DIR/logs",
    "panel_title": "Server Panel"
  },
  "sqlite": {
    "path": "$DB_PATH"
  },
  "admin": {
    "username": "admin",
    "password_hash": "$WEB_HASH"
  },
  "basic_auth": {
    "username": "admin",
    "password_hash": "$BASIC_HASH"
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
log "配置已写入: $CONFIG_FILE"

# ============================================================
# 10. Create systemd service
# ============================================================
log "创建 systemd 服务..."
cat > "$SERVICE_PATH" << EOF
[Unit]
Description=Server Panel — Central Server Management Panel
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
systemctl enable server-panel

# ============================================================
# 11. Start service
# ============================================================
log "启动服务..."
systemctl stop server-panel 2>/dev/null || true
systemctl start server-panel

sleep 2

if systemctl is-active --quiet server-panel; then
    log "服务已启动"
else
    warn "服务可能未正常启动，请检查: journalctl -u server-panel -n 50"
fi

# ============================================================
# 12. Print summary
# ============================================================
echo ""
echo "============================================"
echo "  Server Panel 安装完成"
echo "============================================"
echo ""
echo -e "  ${CYAN}面板地址:${NC}"
if [ "$TLS_MODE" = "acme_http01" ] && [ -n "$DOMAIN" ]; then
    echo -e "    https://${DOMAIN}:${TLS_PORT}/${RANDOM_SUFFIX}/login"
else
    SERVER_IP=$(hostname -I 2>/dev/null | awk '{print $1}' || echo "YOUR_IP")
    echo -e "    https://${SERVER_IP}:${TLS_PORT}/${RANDOM_SUFFIX}/login"
fi
echo ""
echo -e "  ${CYAN}用户名:${NC} admin"
echo -e "  ${CYAN}面板密码:${NC} ${GREEN}${WEB_PASSWORD}${NC}"
echo -e "  ${CYAN}BasicAuth:${NC} admin / ${GREEN}${BASIC_PASSWORD}${NC}"
echo -e "  ${CYAN}TLS 模式:${NC} $TLS_MODE"
if [ "$TLS_MODE" = "acme_http01" ]; then
    echo -e "  ${YELLOW}注意:${NC} 当前使用自签证书，启动后请在系统设置中签发 ACME 证书"
fi
echo ""
echo -e "  ${YELLOW}请立即保存以上密码，尤其是面板密码和 BasicAuth 密码${NC}"
echo ""
echo -e "  管理命令:"
echo -e "    systemctl status server-panel   # 查看状态"
echo -e "    systemctl restart server-panel  # 重启面板"
echo -e "    journalctl -u server-panel -f   # 查看日志"
echo -e "    ${BIN_PATH} --info          # 查看版本信息"
echo ""
