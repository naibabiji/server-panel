#!/bin/bash
set -e

# ============================================================
# Server Panel — 开发环境初始化
# 下载 TailwindCSS CLI + Alpine.js CSP build + Chart.js，编译 CSS
# ============================================================

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

BIN_DIR="$SCRIPT_DIR/bin"
STATIC_JS="$SCRIPT_DIR/static/js"
TAILWIND_VERSION="3.4.4"
ALPINE_VERSION="3.13.7"
CHART_VERSION="4.4.1"

mkdir -p "$BIN_DIR" "$STATIC_JS"

# Detect OS
OS="linux"
if [[ "$(uname)" == "Darwin" ]]; then
    OS="macos"
fi

ARCH="x64"
if [[ "$(uname -m)" == "aarch64" ]]; then
    ARCH="arm64"
fi

# Download TailwindCSS standalone CLI
echo "Downloading TailwindCSS ${TAILWIND_VERSION}..."
if [ "$OS" = "macos" ] && [ "$ARCH" = "arm64" ]; then
    TAILWIND_URL="https://github.com/tailwindlabs/tailwindcss/releases/download/v${TAILWIND_VERSION}/tailwindcss-macos-arm64"
elif [ "$OS" = "macos" ]; then
    TAILWIND_URL="https://github.com/tailwindlabs/tailwindcss/releases/download/v${TAILWIND_VERSION}/tailwindcss-macos-x64"
else
    TAILWIND_URL="https://github.com/tailwindlabs/tailwindcss/releases/download/v${TAILWIND_VERSION}/tailwindcss-linux-x64"
fi
curl -sL "$TAILWIND_URL" -o "$BIN_DIR/tailwindcss"
chmod +x "$BIN_DIR/tailwindcss"

# Download Alpine.js CSP build
echo "Downloading Alpine.js CSP build ${ALPINE_VERSION}..."
curl -sL "https://cdn.jsdelivr.net/npm/@alpinejs/csp@${ALPINE_VERSION}/dist/cdn.min.js" -o "$STATIC_JS/alpine.min.js"

# Download Chart.js
echo "Downloading Chart.js ${CHART_VERSION}..."
curl -sL "https://cdn.jsdelivr.net/npm/chart.js@${CHART_VERSION}/dist/chart.umd.min.js" -o "$STATIC_JS/chart.min.js"

# Compile TailwindCSS
echo "Compiling CSS..."
"$BIN_DIR/tailwindcss" -i input.css -o static/css/main.css --minify

echo ""
echo "Setup complete."
echo "Static files:"
echo "  static/css/main.css"
echo "  static/js/alpine.min.js"
echo "  static/js/chart.min.js"
echo ""
echo "Run: go build -o server-panel ."
