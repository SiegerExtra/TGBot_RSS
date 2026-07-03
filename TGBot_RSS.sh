#!/bin/bash
# TGBot_RSS/SiegerExtra installer/updater with systemd service setup
# Run as:
# chmod +x ./TGBot_RSS.sh
# sudo ./TGBot_RSS.sh

set -e

# Configuration
SERVICE_USER="tgbot-rss"
INSTALL_DIR="/usr/local/tgbot-rss"
BIN_NAME="TGBot_RSS"
REPO_OWNER="SiegerExtra"
REPO_NAME="TGBot_RSS"
API_URL="https://api.github.com/repos/${REPO_OWNER}/${REPO_NAME}/releases/latest"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

e-i() { echo -e "${GREEN}[INFO]${NC} $1"; }
e-w() { echo -e "${YELLOW}[WARN]${NC} $1"; }
e-e() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

# Function to get current installed version
get_installed_version() {
    if [ -f "$INSTALL_DIR/$BIN_NAME" ]; then
        VERSION_OUTPUT=$("$INSTALL_DIR/$BIN_NAME" -version 2>/dev/null || "$INSTALL_DIR/$BIN_NAME" -v 2>/dev/null || echo "")
        echo "$VERSION_OUTPUT" | grep -oE 'v[0-9]+\.[0-9]+\.[0-9]+' | head -1
    else
        echo ""
    fi
}

# Get latest version from GitHub
get_latest_version() {
    curl -s "$API_URL" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/'
}

# Detect OS and architecture
detect_platform() {
    OS=$(uname | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "$OS" in
        linux)
            case "$ARCH" in
                x86_64)   PKG="TGBot_RSS-${VERSION}-linux-amd64.tar.gz" ;;
                aarch64)  PKG="TGBot_RSS-${VERSION}-linux-arm64.tar.gz" ;;
                armv7l)   PKG="TGBot_RSS-${VERSION}-linux-armv7.tar.gz" ;;
                *)        e-e "Unsupported architecture: $ARCH" ;;
            esac
            ;;
        *)
            e-e "Unsupported OS: $OS"
            ;;
    esac
    e-i "Detected: $OS/$ARCH -> $PKG"
}

# Main script
e-i "TGBot_RSS/SiegerExtra Installer/Updater"

# Check if running as root
if [ "$EUID" -ne 0 ]; then
    e-e "Please run as root (sudo ./TGBot_RSS.sh)"
fi

# Get latest version
e-i "Fetching latest version from GitHub..."
VERSION=$(get_latest_version)

if [ -z "$VERSION" ]; then
    e-e "Failed to get latest version. Check network or API rate limits."
fi
e-i "Latest version: $VERSION"

# Check installed version
INSTALLED_VERSION=$(get_installed_version)
if [ -n "$INSTALLED_VERSION" ]; then
    e-i "Installed version: $INSTALLED_VERSION"
else
    e-w "No installed version found or binary not present"
fi

# Compare versions and decide action
if [ -n "$INSTALLED_VERSION" ] && [ "$INSTALLED_VERSION" = "$VERSION" ]; then
    e-i "Already running latest version ($VERSION). No update needed."
    exit 0
else
    e-i "New version available! $INSTALLED_VERSION -> $VERSION"
fi

# Detect platform and set package name
detect_platform

# Download
REPO_URL="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download"
DOWNLOAD_URL="$REPO_URL/$VERSION/$PKG"

e-i "Downloading: $DOWNLOAD_URL"
TEMP_DIR=$(mktemp -d)
cd "$TEMP_DIR"

curl -L -o "$PKG" "$DOWNLOAD_URL" || e-e "Download failed"

# Stop service if running
if systemctl is-active --quiet tgbot-rss.service 2>/dev/null; then
    e-i "Stopping tgbot-rss service..."
    sudo systemctl stop tgbot-rss.service
fi

# Extract
e-i "Extracting..."
if [ -f "$INSTALL_DIR/config.json" ]; then
    e-i "config.json exists, updating TGBot only"
    tar -xzvf "$PKG" "$BIN_NAME" --overwrite 2>/dev/null || tar -xzvf "$PKG" --overwrite
else
    e-i "Fresh install, extracting all files"
    tar -xzvf "$PKG" --overwrite
fi

# Create service user if not exists
if ! id "$SERVICE_USER" &>/dev/null; then
    e-i "Creating service user: $SERVICE_USER"
    useradd -r -s /bin/false "$SERVICE_USER"
fi

# Create install directory
mkdir -p "$INSTALL_DIR"
chown "$SERVICE_USER":"$SERVICE_USER" "$INSTALL_DIR"

# Move files
if [ -f "$BIN_NAME" ]; then
    mv "$BIN_NAME" "$INSTALL_DIR/"
fi
if [ -f config.json ]; then
    # Don't overwrite existing config if it exists
    if [ ! -f "$INSTALL_DIR/config.json" ]; then
        mv config.json "$INSTALL_DIR/"
    else
        e-w "config.json already exists, keeping existing configuration"
        rm -f config.json
    fi
fi
chmod +x "$INSTALL_DIR/$BIN_NAME"

# Fix permissions
chown -R "$SERVICE_USER":"$SERVICE_USER" "$INSTALL_DIR"

# Cleanup
cd /
rm -rf "$TEMP_DIR"
e-i "Cleanup done"

# Create/update systemd service
e-i "Creating/updating systemd service..."
cat > /etc/systemd/system/tgbot-rss.service <<EOF
[Unit]
Description=TGBot_RSS/SiegerExtra Telegram RSS Bot
After=network.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/$BIN_NAME
Restart=always
RestartSec=10
MemoryMax=250M
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

# Reload and restart service
systemctl daemon-reload
if systemctl is-enabled --quiet tgbot-rss.service 2>/dev/null; then
    e-i "Service enabled, starting..."
else
    e-i "Enabling service..."
    systemctl enable tgbot-rss.service
fi
systemctl start tgbot-rss.service

# Show status
e-i "Service status:"
systemctl status tgbot-rss --no-pager || true

e-i ""
e-i "========================================="
e-i "Installation complete!"
e-i ""
e-i "Installed version: $VERSION"
e-i "Binary: $INSTALL_DIR/$BIN_NAME"
e-i "Config: $INSTALL_DIR/config.json"
e-i ""
e-i "Edit config:"
e-i "   sudo -e $INSTALL_DIR/config.json"
e-i ""
e-i "Service commands:"
e-i "   sudo systemctl start tgbot-rss"
e-i "   sudo systemctl stop tgbot-rss"
e-i "   sudo systemctl restart tgbot-rss"
e-i "   sudo systemctl status tgbot-rss"
e-i ""
e-i "View logs:"
e-i "   sudo journalctl -u tgbot-rss -f"
e-i "========================================="