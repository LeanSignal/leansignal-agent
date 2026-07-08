#!/usr/bin/env bash
# LeanSignal Agent installer for Linux and macOS.
#
# Installs the agent (OpenTelemetry Collector) and a co-located VictoriaMetrics,
# then registers them as services (systemd on Linux, launchd on macOS).
#
# Quick start — you only need your agent key + tenant; the gRPC control host and
# the ingest host are derived as <tenant>-grpc.<domain> and <tenant>-ingest.<domain>:
#   curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/install.sh \
#     | sudo bash -s -- --agent-key KEY --tenant TENANT
#
# Advanced: override the derived hosts with --endpoint / --dataplane-endpoint, or
# the domain with --domain (default: eu11.leansignal.io).
#
# Review this script before piping it to a shell.
set -euo pipefail

REPO="${LEANSIGNAL_REPO:-LeanSignal/leansignal-agent}"
VERSION="${VERSION:-latest}"
VM_VERSION_OVERRIDE="${VM_VERSION:-}"
AGENT_KEY=""
AGENT_NAME=""
TENANT=""
DOMAIN="${LEANSIGNAL_DOMAIN:-eu11.leansignal.io}"
ENDPOINT=""
DATAPLANE_ENDPOINT=""
# When CENTRAL_AGENT_GRPC_URL is set (env or --central-url), the agent installs in
# EDGE mode: a lightweight OTLP forwarder to that central agent, with no local VM,
# no tracker/demand filter, and no lean-api control channel. Otherwise CENTRAL mode.
CENTRAL_URL="${CENTRAL_AGENT_GRPC_URL:-}"
INSTALL_VM=1
FROM_UPSTREAM=0

BIN_DIR="/usr/local/bin"

info() { printf '\033[0;36m[leansignal]\033[0m %s\n' "$*"; }
err()  { printf '\033[0;31m[leansignal] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: install.sh --agent-key KEY --agent-name NAME --tenant NAME [options]
       install.sh --agent-key KEY --agent-name NAME --central-url HOST:PORT   (edge)
  --agent-key KEY            Agent authentication key (required)
  --agent-name NAME          Human-friendly name for this agent/host; becomes the
                             agent_name label on every metric (required)
  --central-url HOST:PORT    Install in EDGE mode: forward OTLP to this central
                             agent (plaintext gRPC). Also settable via the
                             CENTRAL_AGENT_GRPC_URL env var. When set, no local VM
                             is installed and --tenant is not required.
  --tenant NAME              Tenant name; derives the gRPC + ingest hosts
                             (required for CENTRAL mode unless --endpoint is given)
  --domain DOMAIN            Cluster domain (default: eu11.leansignal.io)
  --endpoint HOST:PORT       Advanced: gRPC control host, overrides the derived
                             <tenant>-grpc.<domain>:443
  --dataplane-endpoint URL   Advanced: remote-write URL, overrides the derived
                             https://<tenant>-ingest.<domain>/api/v1/write
  --version vX.Y.Z           Agent version to install (default: latest)
  --vm-version X.Y.Z         Override bundled VictoriaMetrics version
  --no-vm                    Do not install the local VictoriaMetrics
  --from-upstream            Pull VictoriaMetrics from upstream instead of the bundle
  -h, --help                 Show this help
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --agent-key) AGENT_KEY="$2"; shift 2;;
    --agent-name) AGENT_NAME="$2"; shift 2;;
    --central-url) CENTRAL_URL="$2"; shift 2;;
    --tenant) TENANT="$2"; shift 2;;
    --domain) DOMAIN="$2"; shift 2;;
    --endpoint) ENDPOINT="$2"; shift 2;;
    --dataplane-endpoint) DATAPLANE_ENDPOINT="$2"; shift 2;;
    --version) VERSION="$2"; shift 2;;
    --vm-version) VM_VERSION_OVERRIDE="$2"; shift 2;;
    --no-vm) INSTALL_VM=0; shift;;
    --from-upstream) FROM_UPSTREAM=1; shift;;
    -h|--help) usage; exit 0;;
    *) err "unknown option: $1";;
  esac
done

# EDGE mode when a central URL is given; it forwards OTLP and runs no local VM.
if [ -n "$CENTRAL_URL" ]; then MODE=edge; INSTALL_VM=0; else MODE=central; fi
info "install mode: ${MODE}"

[ "$(id -u)" -eq 0 ] || err "must run as root (use sudo)"

# --- platform detection ------------------------------------------------------
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  linux) PLATFORM=linux; CONF_DIR=/etc/leansignal-agent; DATA_DIR=/var/lib/leansignal-agent;;
  darwin) PLATFORM=darwin; CONF_DIR=/usr/local/etc/leansignal-agent; DATA_DIR=/usr/local/var/leansignal-agent;;
  *) err "unsupported OS: $os";;
esac
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) ARCH=amd64;;
  arm64|aarch64) ARCH=arm64;;
  *) err "unsupported arch: $arch";;
esac
info "platform: ${PLATFORM}/${ARCH}"

# Prompt for the required connection details when not supplied as flags.
# Reads from /dev/tty so this works even under `curl ... | sudo bash` (where
# stdin is the script itself). Non-interactive runs fall through to the errors below.
prompt_missing() {
  [ -r /dev/tty ] || return 0
  if [ "$MODE" = central ] && [ -z "$ENDPOINT" ] && [ -z "$TENANT" ]; then
    printf 'Tenant name (control host becomes <tenant>-grpc.%s): ' "$DOMAIN" >/dev/tty
    IFS= read -r TENANT </dev/tty || true
  fi
  if [ -z "$AGENT_KEY" ]; then
    printf 'Agent key / secret token (input hidden): ' >/dev/tty
    IFS= read -rs AGENT_KEY </dev/tty || true
    printf '\n' >/dev/tty
  fi
  if [ -z "$AGENT_NAME" ]; then
    printf 'Agent name (identifies this host; becomes the agent_name label): ' >/dev/tty
    IFS= read -r AGENT_NAME </dev/tty || true
  fi
}
prompt_missing

# Required for BOTH modes.
[ -n "$AGENT_KEY" ] || err "agent key is required (--agent-key)"
[ -n "$AGENT_NAME" ] || err "agent name is required (--agent-name)"

if [ "$MODE" = central ]; then
  # The control + ingest hosts are derived from the tenant unless overridden.
  if [ -z "$ENDPOINT" ] || [ -z "$DATAPLANE_ENDPOINT" ]; then
    [ -n "$TENANT" ] || err "tenant is required (--tenant), or pass --endpoint and --dataplane-endpoint explicitly"
    [ -n "$DOMAIN" ] || err "domain is required (--domain)"
  fi
  [ -n "$ENDPOINT" ] || ENDPOINT="${TENANT}-grpc.${DOMAIN}:443"
  [ -n "$DATAPLANE_ENDPOINT" ] || DATAPLANE_ENDPOINT="https://${TENANT}-ingest.${DOMAIN}/api/v1/write"
  info "control endpoint:  ${ENDPOINT}"
  info "dataplane endpoint: ${DATAPLANE_ENDPOINT}"
else
  info "central agent (OTLP): ${CENTRAL_URL}"
fi

# --- resolve version ---------------------------------------------------------
if [ "$VERSION" = "latest" ]; then
  info "resolving latest release..."
  # Capture the API response fully before parsing: piping curl straight into
  # `grep -m1` closes the pipe early, so curl dies with error 23 (write failure)
  # under `set -o pipefail`. A here-string can't SIGPIPE.
  rel_json="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest")"
  VERSION="$(grep -m1 '"tag_name"' <<<"$rel_json" | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')"
  [ -n "$VERSION" ] || err "could not resolve latest version"
fi
VER_NOV="${VERSION#v}"
info "installing version: ${VERSION}"

# --- download bundle ---------------------------------------------------------
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
bundle="leansignal-agent-bundle_${VER_NOV}_${PLATFORM}_${ARCH}.tar.gz"
base="https://github.com/${REPO}/releases/download/${VERSION}"

info "downloading ${bundle}"
curl -fsSL -o "$tmp/$bundle" "${base}/${bundle}" || err "download failed: ${base}/${bundle}"

if curl -fsSL -o "$tmp/bundle-checksums.txt" "${base}/bundle-checksums.txt" 2>/dev/null; then
  ( cd "$tmp" && grep " ./${bundle}\$\| ${bundle}\$" bundle-checksums.txt >/dev/null 2>&1 \
    && { sha256sum -c <(grep "${bundle}" bundle-checksums.txt) 2>/dev/null \
         || shasum -a 256 -c <(grep "${bundle}" bundle-checksums.txt) 2>/dev/null; } ) \
    && info "checksum verified" || info "WARNING: could not verify checksum"
fi

tar -xzf "$tmp/$bundle" -C "$tmp"

# --- install binaries & config ----------------------------------------------
install -d "$CONF_DIR" "$DATA_DIR/vm"
install -m 0755 "$tmp/bin/leansignal-agent" "$BIN_DIR/leansignal-agent"
info "installed $BIN_DIR/leansignal-agent"

if [ "$INSTALL_VM" -eq 1 ]; then
  if [ -f "$tmp/bin/victoria-metrics" ]; then
    install -m 0755 "$tmp/bin/victoria-metrics" "$BIN_DIR/victoria-metrics"
    info "installed $BIN_DIR/victoria-metrics"
  else
    info "WARNING: bundle has no VictoriaMetrics binary; re-run with --from-upstream or --no-vm"
    INSTALL_VM=0
  fi
fi

# pick the config template for this mode (edge ships a separate one in the bundle)
if [ "$MODE" = edge ]; then SRC_CONFIG="$tmp/config/config-edge.yaml"; else SRC_CONFIG="$tmp/config/config.yaml"; fi
[ -f "$SRC_CONFIG" ] || err "bundle is missing $(basename "$SRC_CONFIG") (need a newer release for edge mode)"
# config (do not clobber an existing one)
if [ -f "$CONF_DIR/config.yaml" ]; then
  cp "$SRC_CONFIG" "$CONF_DIR/config.yaml.new"
  info "existing config kept; new template at $CONF_DIR/config.yaml.new"
else
  cp "$SRC_CONFIG" "$CONF_DIR/config.yaml"
fi

# env file (used directly by systemd; substituted into the plist on macOS)
umask 077
if [ "$MODE" = edge ]; then
  cat > "$CONF_DIR/agent.env" <<EOF
LEANSIGNAL_AGENT_KEY=${AGENT_KEY}
LEANSIGNAL_AGENT_NAME=${AGENT_NAME}
CENTRAL_AGENT_GRPC_URL=${CENTRAL_URL}
EOF
else
  cat > "$CONF_DIR/agent.env" <<EOF
LEANSIGNAL_ENDPOINT=${ENDPOINT}
LEANSIGNAL_AGENT_KEY=${AGENT_KEY}
LEANSIGNAL_AGENT_NAME=${AGENT_NAME}
LEANSIGNAL_DATAPLANE_ENDPOINT=${DATAPLANE_ENDPOINT}
EOF
fi
umask 022
info "wrote $CONF_DIR/agent.env (0600)"

# --- services ----------------------------------------------------------------
if [ "$PLATFORM" = linux ]; then
  if [ "$INSTALL_VM" -eq 1 ]; then
    cp "$tmp/service-templates/leansignal-victoria-metrics.service" /etc/systemd/system/
  fi
  cp "$tmp/service-templates/leansignal-agent.service" /etc/systemd/system/
  systemctl daemon-reload
  if [ "$INSTALL_VM" -eq 1 ]; then systemctl enable --now leansignal-victoria-metrics.service; fi
  systemctl enable --now leansignal-agent.service
  info "services started (systemctl status leansignal-agent)"
else
  install -d /usr/local/var/log/leansignal-agent
  if [ "$INSTALL_VM" -eq 1 ]; then
    cp "$tmp/service-templates/com.leansignal.victoria-metrics.plist" /Library/LaunchDaemons/
    launchctl unload /Library/LaunchDaemons/com.leansignal.victoria-metrics.plist 2>/dev/null || true
    launchctl load -w /Library/LaunchDaemons/com.leansignal.victoria-metrics.plist
  fi
  # substitute env values into the agent plist
  sed -e "s|__LEANSIGNAL_ENDPOINT__|${ENDPOINT}|" \
      -e "s|__LEANSIGNAL_AGENT_KEY__|${AGENT_KEY}|" \
      -e "s|__LEANSIGNAL_AGENT_NAME__|${AGENT_NAME}|" \
      -e "s|__LEANSIGNAL_DATAPLANE_ENDPOINT__|${DATAPLANE_ENDPOINT}|" \
      -e "s|__CENTRAL_AGENT_GRPC_URL__|${CENTRAL_URL}|" \
      "$tmp/service-templates/com.leansignal.agent.plist" > /Library/LaunchDaemons/com.leansignal.agent.plist
  chmod 600 /Library/LaunchDaemons/com.leansignal.agent.plist
  launchctl unload /Library/LaunchDaemons/com.leansignal.agent.plist 2>/dev/null || true
  launchctl load -w /Library/LaunchDaemons/com.leansignal.agent.plist
  info "launchd services loaded"
fi

info "done. Local VictoriaMetrics: http://127.0.0.1:8428 ; agent health: http://127.0.0.1:13133"
