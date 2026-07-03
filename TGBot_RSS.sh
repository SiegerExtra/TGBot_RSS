#!/bin/bash
# TGBot_RSS installer with systemd service setup
# Run as:
# chmod +x ./tgbot-rss-install.sh
# sudo ./tgbot-rss-install.sh

set -e

# Configuration
SERVICE_USER="tgbot-rss"
INSTALL_DIR="/usr/local/tgbot-rss"
BIN_NAME="TGBot_RSS"
API_URL="https://api.github.com/repos/IonRh/TGBot_RSS/releases/latest"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

e-i() { echo -e "${GREEN}[INFO]${NC} $1"; }
e-w() { echo -e "${YELLOW}[WARN]${NC} $1"; }
e-e() { echo -e "${RED}[ERROR]${NC} $1"; exit 1; }

# Get latest version
e-i "Fetching latest version..."
VERSION=$(curl -s "$API_URL" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

if [ -z "$VERSION" ]; then
    e-e "Failed to get latest version"
fi
e-i "Latest version: $VERSION"

# Detect OS and architecture
OS=$(uname | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$OS" in
    linux)
        case "$ARCH" in
            x86_64)   PKG="TGBot-linux-amd64.tar.gz" ;;
            aarch64)  PKG="TGBot-linux-arm64.tar.gz" ;;
            armv7l)   PKG="TGBot-linux-armv7.tar.gz" ;;
            *)        e-e "Unsupported architecture: $ARCH" ;;
        esac
        ;;
    *)
        e-e "Unsupported OS: $OS"
        ;;
esac
e-i "Detected: $OS/$ARCH -> $PKG"

# Download
REPO_URL="https://github.com/IonRh/TGBot_RSS/releases/download"
DOWNLOAD_URL="$REPO_URL/$VERSION/$PKG"

e-i "Downloading: $DOWNLOAD_URL"
TEMP_DIR=$(mktemp -d)
cd "$TEMP_DIR"

curl -L -o "$PKG" "$DOWNLOAD_URL" || e-e "Download failed"

# Extract
e-i "Extracting..."
if [ -f "$INSTALL_DIR/config.json" ]; then
    e-i "config.json exists, updating TGBot only"
    tar -xzvf "$PKG" TGBot_RSS --overwrite
else
    e-i "Fresh install, extracting all files"
    tar -xzvf "$PKG" --overwrite
fi

# Create service user if not exists
if ! id "$SERVICE_USER" &>/dev/null; then
    e-i "Creating service user: $SERVICE_USER"
    sudo useradd -r -s /bin/false "$SERVICE_USER"
fi

# Create install directory
sudo mkdir -p "$INSTALL_DIR"
sudo chown "$SERVICE_USER":"$SERVICE_USER" "$INSTALL_DIR"

# Move files
sudo mv TGBot_RSS "$INSTALL_DIR/"
if [ -f config.json ]; then
    sudo mv config.json "$INSTALL_DIR/"
fi
sudo chmod +x "$INSTALL_DIR/TGBot_RSS"

# Fix permissions
sudo chown -R "$SERVICE_USER":"$SERVICE_USER" "$INSTALL_DIR"

# Cleanup
cd /
rm -rf "$TEMP_DIR"
e-i "Cleanup done"

# Create systemd service
e-i "Creating systemd service..."
sudo tee /etc/systemd/system/tgbot-rss.service > /dev/null <<EOF
[Unit]
Description=TGBot_RSS Telegram RSS Bot
After=network.target

[Service]
Type=simple
User=$SERVICE_USER
Group=$SERVICE_USER
WorkingDirectory=$INSTALL_DIR
ExecStart=$INSTALL_DIR/TGBot_RSS
Restart=always
RestartSec=10
MemoryMax=250M

[Install]
WantedBy=multi-user.target
EOF

# Enable and start service
# e-i "Enabling and starting service..."
sudo systemctl daemon-reload
# sudo systemctl enable tgbot-rss.service
# sudo systemctl start tgbot-rss.service

# Status
e-i "Service status:"
sudo systemctl status tgbot-rss --no-pager

e-i ""
e-i "========================================="
e-i "Installation complete!"
e-i ""
e-i "Binary installed to: $INSTALL_DIR/TGBot_RSS"
e-i "Config file: $INSTALL_DIR/config.json"
e-i "Edit config:"
e-i "   sudo -e $INSTALL_DIR/config.json"
e-i "Service actions:"
e-i "   sudo systemctl daemon-reload"
e-i "   sudo systemctl enable --now tgbot-rss"
e-i "   sudo systemctl disable --now tgbot-rss"
e-i ""
e-i "   sudo systemctl status tgbot-rss"
e-i ""
e-i "View logs:"
e-i "   sudo journalctl -u tgbot-rss -f"
e-i "========================================="