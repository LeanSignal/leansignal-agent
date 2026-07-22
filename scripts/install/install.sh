#!/usr/bin/env bash
# LeanSignal Agent installer for Linux and macOS.
#
# Installs the agent (OpenTelemetry Collector), a co-located VictoriaMetrics
# (metrics, from the release bundle), a co-located Loki (logs, from the
# grafana/loki GitHub release; Linux only) and a co-located Tempo (traces,
# from the grafana/tempo GitHub release; Linux only), then registers them as
# services (systemd on Linux, launchd on macOS).
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
# Pinned Loki version for the co-located local log store. Keep in sync with the
# LOKI_VERSION file at the repo root (the single source of truth for the pin).
LOKI_VERSION_DEFAULT="3.5.12"
LOKI_VERSION="${LOKI_VERSION:-$LOKI_VERSION_DEFAULT}"
# Pinned Tempo version for the co-located local trace store. Keep in sync with
# the TEMPO_VERSION file at the repo root.
TEMPO_VERSION_DEFAULT="2.7.1"
TEMPO_VERSION="${TEMPO_VERSION:-$TEMPO_VERSION_DEFAULT}"
AGENT_KEY=""
AGENT_NAME=""
TENANT=""
# Region domain. Empty by default: the agent RESOLVES the tenant's region from
# control-center at startup (${leansignal:...} provider). Set --domain (or the
# LEANSIGNAL_DOMAIN env) only to pin the region and SKIP that lookup.
DOMAIN="${LEANSIGNAL_DOMAIN:-}"
# control-center resolve tuning (optional; sensible defaults live in the agent).
CC_URL="${LEANSIGNAL_CC_URL:-}"
RESOLVE_AAT="${LEANSIGNAL_RESOLVE_AAT:-}"
# Optional per-host pins (BASE URLs); any left empty is derived from the slug.
ENDPOINT=""
DATAPLANE_ENDPOINT=""
LOKI_ENDPOINT=""
TEMPO_ENDPOINT=""
# When CENTRAL_AGENT_GRPC_URL is set (env or --central-url), the agent installs in
# EDGE mode: a lightweight OTLP forwarder to that central agent, with no local VM,
# no local Loki/Tempo, no tracker/demand filter, and no lean-api control channel.
# Otherwise CENTRAL mode.
CENTRAL_URL="${CENTRAL_AGENT_GRPC_URL:-}"
INSTALL_VM=1
INSTALL_LOKI="${INSTALL_LOKI:-1}"
INSTALL_TEMPO="${INSTALL_TEMPO:-1}"
FROM_UPSTREAM=0
BUNDLE_FILE="${BUNDLE_FILE:-}"

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
  --tenant SLUG              Tenant slug (e.g. "lean"). The agent resolves the
                             tenant's region from control-center at startup and
                             derives the gRPC + per-signal ingest hosts from it
                             (required for CENTRAL mode unless the hosts are pinned)
  --domain DOMAIN            Advanced: pin the region domain (e.g. eu11.leansignal.io)
                             and SKIP the control-center region lookup
  --cc-url URL               Advanced: control-center origin (default https://cc.leansignal.io)
  --resolve-aat TOKEN        Advanced: control-center resolve token (has a public default)
  --endpoint HOST:PORT       Advanced: pin the gRPC control host (skips resolution
                             for it), e.g. <tenant>-grpc.<domain>:443
  --dataplane-endpoint URL   Advanced: pin the metrics-ingest BASE URL
                             (exporter appends /api/v1/write)
  --loki-endpoint URL        Advanced: pin the logs-ingest BASE URL
                             (exporter appends /otlp/v1/logs)
  --tempo-endpoint URL       Advanced: pin the traces-ingest BASE URL
                             (exporter appends /v1/traces)
  --version vX.Y.Z           Agent version to install (default: latest)
  --bundle FILE              Install from a local bundle tar.gz (e.g. built by
                             scripts/release/build-bundles.sh) instead of
                             downloading a GitHub release
  --vm-version X.Y.Z         Override bundled VictoriaMetrics version
  --loki-version X.Y.Z       Override the pinned Loki version
  --tempo-version X.Y.Z      Override the pinned Tempo version
  --no-vm                    Do not install the local VictoriaMetrics
  --no-loki                  Do not install the local Loki (log store)
  --no-tempo                 Do not install the local Tempo (trace store)
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
    --cc-url) CC_URL="$2"; shift 2;;
    --resolve-aat) RESOLVE_AAT="$2"; shift 2;;
    --endpoint) ENDPOINT="$2"; shift 2;;
    --dataplane-endpoint) DATAPLANE_ENDPOINT="$2"; shift 2;;
    --loki-endpoint) LOKI_ENDPOINT="$2"; shift 2;;
    --tempo-endpoint) TEMPO_ENDPOINT="$2"; shift 2;;
    --version) VERSION="$2"; shift 2;;
    --vm-version) VM_VERSION_OVERRIDE="$2"; shift 2;;
    --loki-version) LOKI_VERSION="$2"; shift 2;;
    --tempo-version) TEMPO_VERSION="$2"; shift 2;;
    --bundle) BUNDLE_FILE="$2"; shift 2;;
    --no-vm) INSTALL_VM=0; shift;;
    --no-loki) INSTALL_LOKI=0; shift;;
    --no-tempo) INSTALL_TEMPO=0; shift;;
    --from-upstream) FROM_UPSTREAM=1; shift;;
    -h|--help) usage; exit 0;;
    *) err "unknown option: $1";;
  esac
done

# EDGE mode when a central URL is given; it forwards OTLP and runs no local VM/Loki/Tempo.
if [ -n "$CENTRAL_URL" ]; then MODE=edge; INSTALL_VM=0; INSTALL_LOKI=0; INSTALL_TEMPO=0; else MODE=central; fi
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
    printf 'Tenant slug (the agent resolves its region + hosts from control-center): ' >/dev/tty
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
  # The agent resolves the tenant's region from control-center at startup and
  # derives every backend host from the slug via the ${leansignal:...} config
  # provider. So all we need is the tenant slug — unless the operator pins the
  # hosts explicitly (each pin skips resolution for that one host). A tenant slug
  # is still required unless BOTH the gRPC and metrics hosts are pinned (the two
  # the agent can't run without).
  if { [ -z "$ENDPOINT" ] || [ -z "$DATAPLANE_ENDPOINT" ]; } && [ -z "$TENANT" ]; then
    err "tenant is required (--tenant), or pin the hosts explicitly (--endpoint and --dataplane-endpoint)"
  fi
  if [ -n "$TENANT" ]; then
    if [ -n "$DOMAIN" ]; then
      info "tenant: ${TENANT}  (region pinned: ${DOMAIN} — control-center lookup skipped)"
    else
      info "tenant: ${TENANT}  (region resolved from control-center at startup)"
    fi
  fi
  [ -n "$ENDPOINT" ]           && info "gRPC host pinned:    ${ENDPOINT}"
  [ -n "$DATAPLANE_ENDPOINT" ] && info "metrics host pinned: ${DATAPLANE_ENDPOINT}"
  [ -n "$LOKI_ENDPOINT" ]      && info "logs host pinned:    ${LOKI_ENDPOINT}"
  [ -n "$TEMPO_ENDPOINT" ]     && info "traces host pinned:  ${TEMPO_ENDPOINT}"
else
  info "central agent (OTLP): ${CENTRAL_URL}"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if [ -n "$BUNDLE_FILE" ]; then
  # --- local bundle ------------------------------------------------------------
  [ -f "$BUNDLE_FILE" ] || err "bundle not found: ${BUNDLE_FILE}"
  info "installing from local bundle: ${BUNDLE_FILE}"
  tar -xzf "$BUNDLE_FILE" -C "$tmp"
else
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
fi

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

# Local Loki (log store) - pulled from the grafana/loki GitHub release (pinned
# LOKI_VERSION), Linux only for now (no launchd plist yet - macOS installs skip it).
if [ "$INSTALL_LOKI" -eq 1 ] && [ "$PLATFORM" != linux ]; then
  info "NOTE: local Loki install is Linux-only for now; skipping on ${PLATFORM}"
  INSTALL_LOKI=0
fi
if [ "$INSTALL_LOKI" -eq 1 ] && ! command -v unzip >/dev/null 2>&1; then
  info "WARNING: unzip not found (needed to extract the Loki release); skipping local Loki. Install unzip and re-run, or use --no-loki to silence this."
  INSTALL_LOKI=0
fi
if [ "$INSTALL_LOKI" -eq 1 ] && { [ ! -f "$tmp/service-templates/loki.yaml" ] || [ ! -f "$tmp/service-templates/leansignal-loki.service" ]; }; then
  info "WARNING: bundle has no Loki config/service templates (need a newer release); skipping local Loki"
  INSTALL_LOKI=0
fi
if [ "$INSTALL_LOKI" -eq 1 ]; then
  loki_zip="loki-linux-${ARCH}.zip"
  loki_url="https://github.com/grafana/loki/releases/download/v${LOKI_VERSION}/${loki_zip}"
  info "downloading Loki v${LOKI_VERSION} (${loki_zip})"
  curl -fsSL -o "$tmp/$loki_zip" "$loki_url" || err "download failed: ${loki_url}"
  unzip -oq "$tmp/$loki_zip" -d "$tmp/loki-extract"
  install -m 0755 "$tmp/loki-extract/loki-linux-${ARCH}" "$BIN_DIR/loki"
  install -d "$DATA_DIR/loki"
  info "installed $BIN_DIR/loki"
fi

# Local Tempo (trace store) - pulled from the grafana/tempo GitHub release
# (pinned TEMPO_VERSION), Linux only for now (no launchd plist yet).
if [ "$INSTALL_TEMPO" -eq 1 ] && [ "$PLATFORM" != linux ]; then
  info "NOTE: local Tempo install is Linux-only for now; skipping on ${PLATFORM}"
  INSTALL_TEMPO=0
fi
if [ "$INSTALL_TEMPO" -eq 1 ] && { [ ! -f "$tmp/service-templates/tempo.yaml" ] || [ ! -f "$tmp/service-templates/leansignal-tempo.service" ]; }; then
  info "WARNING: bundle has no Tempo config/service templates (need a newer release); skipping local Tempo"
  INSTALL_TEMPO=0
fi
if [ "$INSTALL_TEMPO" -eq 1 ]; then
  tempo_tar="tempo_${TEMPO_VERSION}_linux_${ARCH}.tar.gz"
  tempo_url="https://github.com/grafana/tempo/releases/download/v${TEMPO_VERSION}/${tempo_tar}"
  info "downloading Tempo v${TEMPO_VERSION} (${tempo_tar})"
  curl -fsSL -o "$tmp/$tempo_tar" "$tempo_url" || err "download failed: ${tempo_url}"
  mkdir -p "$tmp/tempo-extract"
  tar -xzf "$tmp/$tempo_tar" -C "$tmp/tempo-extract"
  install -m 0755 "$tmp/tempo-extract/tempo" "$BIN_DIR/tempo"
  install -d "$DATA_DIR/tempo"
  info "installed $BIN_DIR/tempo"
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

# local Loki config (same no-clobber rule)
if [ "$INSTALL_LOKI" -eq 1 ]; then
  if [ -f "$CONF_DIR/loki.yaml" ]; then
    cp "$tmp/service-templates/loki.yaml" "$CONF_DIR/loki.yaml.new"
    info "existing loki config kept; new template at $CONF_DIR/loki.yaml.new"
  else
    cp "$tmp/service-templates/loki.yaml" "$CONF_DIR/loki.yaml"
  fi
fi

# local Tempo config (same no-clobber rule)
if [ "$INSTALL_TEMPO" -eq 1 ]; then
  if [ -f "$CONF_DIR/tempo.yaml" ]; then
    cp "$tmp/service-templates/tempo.yaml" "$CONF_DIR/tempo.yaml.new"
    info "existing tempo config kept; new template at $CONF_DIR/tempo.yaml.new"
  else
    cp "$tmp/service-templates/tempo.yaml" "$CONF_DIR/tempo.yaml"
  fi
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
  # The tenant slug drives the ${leansignal:...} provider's startup resolve; the
  # backend hosts are NOT written here unless explicitly pinned (each pin becomes
  # an override the provider returns verbatim, skipping resolution for that host).
  {
    printf 'LEANSIGNAL_TENANT=%s\n' "${TENANT}"
    printf 'LEANSIGNAL_AGENT_KEY=%s\n' "${AGENT_KEY}"
    printf 'LEANSIGNAL_AGENT_NAME=%s\n' "${AGENT_NAME}"
    if [ -n "$CC_URL" ];             then printf 'LEANSIGNAL_CC_URL=%s\n' "${CC_URL}"; fi
    if [ -n "$RESOLVE_AAT" ];        then printf 'LEANSIGNAL_RESOLVE_AAT=%s\n' "${RESOLVE_AAT}"; fi
    if [ -n "$DOMAIN" ];             then printf 'LEANSIGNAL_DOMAIN=%s\n' "${DOMAIN}"; fi
    if [ -n "$ENDPOINT" ];           then printf 'LEANSIGNAL_ENDPOINT=%s\n' "${ENDPOINT}"; fi
    if [ -n "$DATAPLANE_ENDPOINT" ]; then printf 'LEANSIGNAL_DATAPLANE_ENDPOINT=%s\n' "${DATAPLANE_ENDPOINT}"; fi
    if [ -n "$LOKI_ENDPOINT" ];      then printf 'LEANSIGNAL_LOKI_ENDPOINT=%s\n' "${LOKI_ENDPOINT}"; fi
    if [ -n "$TEMPO_ENDPOINT" ];     then printf 'LEANSIGNAL_TEMPO_ENDPOINT=%s\n' "${TEMPO_ENDPOINT}"; fi
  } > "$CONF_DIR/agent.env"
fi
umask 022
info "wrote $CONF_DIR/agent.env (0600)"

# --- services ----------------------------------------------------------------
if [ "$PLATFORM" = linux ]; then
  if [ "$INSTALL_VM" -eq 1 ]; then
    cp "$tmp/service-templates/leansignal-victoria-metrics.service" /etc/systemd/system/
  fi
  if [ "$INSTALL_LOKI" -eq 1 ]; then
    cp "$tmp/service-templates/leansignal-loki.service" /etc/systemd/system/
  fi
  if [ "$INSTALL_TEMPO" -eq 1 ]; then
    cp "$tmp/service-templates/leansignal-tempo.service" /etc/systemd/system/
  fi
  cp "$tmp/service-templates/leansignal-agent.service" /etc/systemd/system/
  systemctl daemon-reload
  if [ "$INSTALL_VM" -eq 1 ]; then systemctl enable --now leansignal-victoria-metrics.service; fi
  if [ "$INSTALL_LOKI" -eq 1 ]; then systemctl enable --now leansignal-loki.service; fi
  if [ "$INSTALL_TEMPO" -eq 1 ]; then systemctl enable --now leansignal-tempo.service; fi
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
  sed -e "s|__LEANSIGNAL_TENANT__|${TENANT}|" \
      -e "s|__LEANSIGNAL_ENDPOINT__|${ENDPOINT}|" \
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

summary="done. Local VictoriaMetrics: http://127.0.0.1:8428"
[ "$INSTALL_LOKI" -eq 1 ] && summary="${summary} ; local Loki: http://127.0.0.1:3100"
[ "$INSTALL_TEMPO" -eq 1 ] && summary="${summary} ; local Tempo: http://127.0.0.1:3200"
info "${summary} ; agent health: http://127.0.0.1:13133"
