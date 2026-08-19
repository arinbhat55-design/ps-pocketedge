#!/bin/sh
# Installs the PSpocketEdge agent as a systemd service.
#
# Usage:
#   curl -sSL https://<control-plane>/install.sh | sh -s -- \
#     --server=<control-plane-host>:8443 --token=<enrollment-token>
#
# The --token=<value> form is the quick-start default but leaves the token
# visible in shell history and `ps` output on this machine. Prefer setting
# PE_ENROLL_TOKEN instead:
#   curl -sSL https://<control-plane>/install.sh | PE_ENROLL_TOKEN=<token> sh -s -- --server=<host>:8443
#
# For local testing before any GitHub release exists, use
# --local-binary=/path/to/pe-agent to install an already-built binary
# instead of downloading one.

set -eu

VERSION="latest"
REPO="ankitapaul1586-cmd/pspocketedge"
SERVER=""
TOKEN="${PE_ENROLL_TOKEN:-}"
LOCAL_BINARY=""
INSTALL_DIR="/usr/local/bin"
CONFIG_DIR="/etc/pspocketedge"

for arg in "$@"; do
  case "$arg" in
    --server=*) SERVER="${arg#*=}" ;;
    --token=*) TOKEN="${arg#*=}" ;;
    --version=*) VERSION="${arg#*=}" ;;
    --local-binary=*) LOCAL_BINARY="${arg#*=}" ;;
    *) echo "unknown argument: $arg" >&2; exit 1 ;;
  esac
done

if [ -z "$SERVER" ]; then
  echo "error: --server=<host>:<port> is required" >&2
  exit 1
fi
if [ -z "$TOKEN" ]; then
  echo "error: an enrollment token is required (--token=... or PE_ENROLL_TOKEN env var)" >&2
  exit 1
fi

if [ "$(id -u)" -ne 0 ]; then
  echo "error: this script must be run as root (it installs a systemd service)" >&2
  exit 1
fi

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
if [ "$OS" != "linux" ]; then
  echo "error: only linux is supported (got $OS)" >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64) ARCH="amd64" ;;
  aarch64|arm64) ARCH="arm64" ;;
  armv7l|armv6l) ARCH="armv7" ;;
  *) echo "error: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

echo "==> installing pe-agent binary"
if [ -n "$LOCAL_BINARY" ]; then
  install -m 0755 "$LOCAL_BINARY" "$INSTALL_DIR/pe-agent"
else
  ASSET="pe-agent_${VERSION}_linux_${ARCH}.tar.gz"
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  echo "    downloading $URL"
  curl -sSL "$URL" -o "$TMP/agent.tar.gz"
  tar -xzf "$TMP/agent.tar.gz" -C "$TMP" pe-agent
  install -m 0755 "$TMP/pe-agent" "$INSTALL_DIR/pe-agent"
fi

echo "==> writing $CONFIG_DIR/agent.yaml"
mkdir -p "$CONFIG_DIR"
chmod 700 "$CONFIG_DIR"
cat > "$CONFIG_DIR/agent.yaml" <<EOF
server: "$SERVER"
token: "$TOKEN"
state_path: "$CONFIG_DIR/state.json"
EOF
chmod 600 "$CONFIG_DIR/agent.yaml"

echo "==> installing systemd unit"
cat > /etc/systemd/system/pe-agent.service <<'EOF'
[Unit]
Description=PSpocketEdge agent
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/pe-agent --config=/etc/pspocketedge/agent.yaml
Restart=on-failure
RestartSec=5s
User=root

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now pe-agent.service

echo "==> done. check status with: systemctl status pe-agent.service"
echo "    follow logs with:        journalctl -u pe-agent.service -f"
