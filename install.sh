#!/bin/bash
set -e

REPO="lucasnevespereira/nevinho"
INSTALL_DIR="$HOME/.local/bin"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)

case "$ARCH" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

case "$OS" in
  linux|darwin) ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

BINARY="nevinho-${OS}-${ARCH}"

echo "Fetching latest release..."
LATEST=$(curl -sSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | cut -d'"' -f4)
if [ -z "$LATEST" ]; then
  echo "No releases found. Check https://github.com/${REPO}/releases"
  exit 1
fi

echo "Downloading nevinho ${LATEST} (${OS}/${ARCH})..."
if ! curl -fsSL "https://github.com/${REPO}/releases/download/${LATEST}/${BINARY}" -o /tmp/nevinho; then
  echo "Download failed. Binary may not exist for ${OS}/${ARCH}."
  exit 1
fi
chmod +x /tmp/nevinho

mkdir -p "$INSTALL_DIR"
mv /tmp/nevinho "$INSTALL_DIR/nevinho"
echo "Installed to ${INSTALL_DIR}/nevinho"

# Add to PATH if not already there
if ! echo "$PATH" | grep -qF "$INSTALL_DIR"; then
  SHELL_RC="$HOME/.bashrc"
  [ -f "$HOME/.zshrc" ] && SHELL_RC="$HOME/.zshrc"
  touch "$SHELL_RC"
  echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$SHELL_RC"
  echo "Added ~/.local/bin to PATH in $(basename "$SHELL_RC")"
  echo ""
  echo "Run this to start using nevinho now:"
  echo ""
  echo "  exec \$SHELL"
  echo ""
fi

echo "Next steps:"
echo "  nevinho --setup    configure Discord token and LLM keys"
echo "  nevinho             start the bot"
