#!/bin/sh
# Install the agent in the current macOS user's launchd session.
# Start Colima or Podman machine before installing.
# Usage: PE_ENROLL_TOKEN=... sh scripts/install-agent-macos.sh --server=host:8443 [--runtime=docker|podman] [--allow-builds]
set -eu

REPO="ankitapaul1586-cmd/pspocketedge"
VERSION="latest"
SERVER=""
TOKEN="${PE_ENROLL_TOKEN:-}"
LOCAL_BINARY=""
CA_FILE=""
ALLOW_BUILDS="false"
CONTAINER_RUNTIME="docker"
CONTAINER_HOST="${DOCKER_HOST:-}"

for arg in "$@"; do
  case "$arg" in
    --server=*) SERVER="${arg#*=}" ;;
    --token=*) TOKEN="${arg#*=}" ;;
    --version=*) VERSION="${arg#*=}" ;;
    --local-binary=*) LOCAL_BINARY="${arg#*=}" ;;
    --ca-file=*) CA_FILE="${arg#*=}" ;;
    --runtime=*) CONTAINER_RUNTIME="${arg#*=}" ;;
    --container-host=*) CONTAINER_HOST="${arg#*=}" ;;
    --allow-builds) ALLOW_BUILDS="true" ;;
    *) echo "unknown argument: $arg" >&2; exit 1 ;;
  esac
done

if [ "$(uname -s)" != "Darwin" ]; then
  echo "error: this installer is for macOS" >&2
  exit 1
fi
if [ "$(id -u)" -eq 0 ]; then
  echo "error: run as your macOS user, without sudo" >&2
  exit 1
fi
if [ -z "$SERVER" ] || [ -z "$TOKEN" ]; then
  echo "error: --server=host:port and PE_ENROLL_TOKEN are required" >&2
  exit 1
fi
case "$SERVER" in
  *[!a-zA-Z0-9.:-]*|'') echo "error: invalid server address" >&2; exit 1 ;;
esac

case "$CONTAINER_RUNTIME" in
  docker)
    CONTAINER_HOST="${CONTAINER_HOST:-unix://${HOME}/.colima/default/docker.sock}"
    ;;
  podman)
    if [ -z "$CONTAINER_HOST" ]; then
      if ! command -v podman >/dev/null 2>&1; then
        echo "error: install Podman and start podman machine first" >&2; exit 1
      fi
      SOCKET="$(podman machine inspect --format '{{.ConnectionInfo.PodmanSocket.Path}}')"
      case "$SOCKET" in
        ''|*'
'*) echo "error: expected one Podman machine socket; start your machine or pass --container-host=unix:///path/to/socket" >&2; exit 1 ;;
        /*) ;;
        *) echo "error: no valid Podman machine socket; run podman machine start" >&2; exit 1 ;;
      esac
      CONTAINER_HOST="unix://$SOCKET"
    fi
    ;;
  *) echo "error: --runtime must be docker or podman" >&2; exit 1 ;;
esac
export DOCKER_HOST="$CONTAINER_HOST"
case "$CONTAINER_HOST" in
  unix://*)
    if ! curl -fsS --unix-socket "${CONTAINER_HOST#unix://}" http://localhost/_ping >/dev/null; then
      echo "error: cannot connect to $CONTAINER_HOST; start your runtime first" >&2; exit 1
    fi
    ;;
  *) echo "error: macOS installer requires a unix:// container socket" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64) ARCH="amd64" ;;
  arm64) ARCH="arm64" ;;
  *) echo "error: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

BIN_DIR="$HOME/.local/bin"
CONFIG_DIR="$HOME/Library/Application Support/PSpocketEdge"
PLIST="$HOME/Library/LaunchAgents/com.pspocketedge.agent.plist"
mkdir -p "$BIN_DIR" "$CONFIG_DIR" "$HOME/Library/LaunchAgents"
chmod 700 "$CONFIG_DIR"

if [ -n "$LOCAL_BINARY" ]; then
  install -m 0755 "$LOCAL_BINARY" "$BIN_DIR/pe-agent"
else
  if [ "$VERSION" = "latest" ]; then
    RELEASE_URL="$(curl -fLsS -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")"
    VERSION="${RELEASE_URL##*/}"
  fi
  ASSET_VERSION="${VERSION#v}"
  ASSET="pe-agent_${ASSET_VERSION}_darwin_${ARCH}.tar.gz"
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  curl -fLsS "$URL" -o "$TMP/agent.tar.gz"
  tar -xzf "$TMP/agent.tar.gz" -C "$TMP" pe-agent
  install -m 0755 "$TMP/pe-agent" "$BIN_DIR/pe-agent"
fi

if [ -n "$CA_FILE" ]; then
  if [ ! -r "$CA_FILE" ]; then
    echo "error: CA file is not readable: $CA_FILE" >&2
    exit 1
  fi
  install -m 0600 "$CA_FILE" "$CONFIG_DIR/control-plane-ca.pem"
fi
cat > "$CONFIG_DIR/agent.yaml" <<EOF
server: "$SERVER"
token: "$TOKEN"
state_path: "$CONFIG_DIR/state.json"
tls: true
allow_builds: $ALLOW_BUILDS
container_runtime: "$CONTAINER_RUNTIME"
EOF
if [ -n "$CA_FILE" ]; then
  echo "tls_ca_file: \"$CONFIG_DIR/control-plane-ca.pem\"" >> "$CONFIG_DIR/agent.yaml"
fi
chmod 600 "$CONFIG_DIR/agent.yaml"

# The agent SDK and Docker Buildx both read DOCKER_HOST from this environment.
# Include Homebrew locations so Buildx is found when launchd starts the agent.
xml_escape() {
  printf '%s' "$1" | sed -e 's/\&/\&amp;/g' -e 's/</\&lt;/g' -e 's/>/\&gt;/g' -e 's/"/\&quot;/g' -e "s/'/\&apos;/g"
}
XML_BIN_DIR="$(xml_escape "$BIN_DIR")"
XML_CONFIG_DIR="$(xml_escape "$CONFIG_DIR")"
XML_DOCKER_HOST="$(xml_escape "$DOCKER_HOST")"
cat > "$PLIST" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>com.pspocketedge.agent</string>
  <key>ProgramArguments</key><array>
    <string>$XML_BIN_DIR/pe-agent</string>
    <string>--config=$XML_CONFIG_DIR/agent.yaml</string>
  </array>
  <key>EnvironmentVariables</key><dict>
    <key>DOCKER_HOST</key><string>$XML_DOCKER_HOST</string>
    <key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:$XML_BIN_DIR</string>
  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>StandardOutPath</key><string>$XML_CONFIG_DIR/agent.log</string>
  <key>StandardErrorPath</key><string>$XML_CONFIG_DIR/agent.log</string>
</dict></plist>
EOF
chmod 600 "$PLIST"
launchctl bootout "gui/$(id -u)/com.pspocketedge.agent" 2>/dev/null || true
launchctl bootstrap "gui/$(id -u)" "$PLIST"
echo "Agent installed. Check logs at $CONFIG_DIR/agent.log"
