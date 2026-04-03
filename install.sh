#!/bin/bash
set -e

REPO="lucasnevespereira/nevinho"

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

# Try /usr/local/bin first (in PATH everywhere), fall back to ~/.local/bin
if [ -w /usr/local/bin ]; then
  mv /tmp/nevinho /usr/local/bin/nevinho
  echo "Installed to /usr/local/bin/nevinho"
else
  INSTALL_DIR="$HOME/.local/bin"
  mkdir -p "$INSTALL_DIR"
  mv /tmp/nevinho "$INSTALL_DIR/nevinho"
  echo "Installed to $INSTALL_DIR/nevinho"
  if ! echo "$PATH" | grep -qF "$INSTALL_DIR"; then
    SHELL_RC="$HOME/.bashrc"
    [ -f "$HOME/.zshrc" ] && SHELL_RC="$HOME/.zshrc"
    touch "$SHELL_RC"
    echo 'export PATH="$HOME/.local/bin:$PATH"' >> "$SHELL_RC"
    echo "Run 'source $SHELL_RC' to update your PATH."
  fi
fi

echo ""
echo "Done! Run:"
echo "  nevinho --setup"
echo "  nevinho"
