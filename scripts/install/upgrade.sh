#!/usr/bin/env bash
# LeanSignal Agent upgrader for Linux and macOS.
#
# Upgrades an EXISTING install in place. By default only the agent (the
# OpenTelemetry Collector binary) is upgraded: it stops just the agent service,
# swaps the binary, and starts it again. VictoriaMetrics — and, crucially, its
# on-disk data directory — is never touched, so no metrics are lost.
#
# With --with-vm it ALSO upgrades the co-located VictoriaMetrics: it takes an
# instant VM snapshot first (and ABORTS if the snapshot can't be confirmed),
# then swaps the VM binary against the SAME --storageDataPath, so existing data
# is preserved. The snapshot it created is removed once the upgrade is healthy.
#
# Either operation rolls the binary back automatically if the service does not
# come back healthy.
#
#   # agent -> latest release, VM untouched (the common case)
#   curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/upgrade.sh | sudo bash
#
#   # agent -> a specific version
#   curl -fsSL .../upgrade.sh | sudo bash -s -- --version v0.2.0
#
#   # also upgrade VictoriaMetrics to the version this release ships (snapshots first)
#   curl -fsSL .../upgrade.sh | sudo bash -s -- --with-vm
#
# Review this script before piping it to a shell.
set -euo pipefail

REPO="${LEANSIGNAL_REPO:-LeanSignal/leansignal-agent}"
VERSION="${VERSION:-latest}"
VM_VERSION_OVERRIDE="${VM_VERSION:-}"
WITH_VM=0
SKIP_SNAPSHOT=0
BIN_DIR="/usr/local/bin"

info() { printf '\033[0;36m[leansignal]\033[0m %s\n' "$*"; }
warn() { printf '\033[0;33m[leansignal] WARNING:\033[0m %s\n' "$*" >&2; }
err()  { printf '\033[0;31m[leansignal] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
Usage: upgrade.sh [options]
  --version vX.Y.Z     Agent version to upgrade to (default: latest release)
  --with-vm            Also upgrade the co-located VictoriaMetrics. Snapshots
                       first (aborts if the snapshot can't be confirmed); the
                       data directory is preserved. Off by default.
  --vm-version X.Y.Z   With --with-vm: force a specific VictoriaMetrics version
                       (default: the version this agent release ships)
  --skip-snapshot      With --with-vm: skip the pre-upgrade VM snapshot (accept
                       the risk of an in-place VM format change with no rollback)
  -h, --help           Show this help

By DEFAULT only the agent is upgraded — VictoriaMetrics and its data are untouched.
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --version) VERSION="$2"; shift 2;;
    --with-vm) WITH_VM=1; shift;;
    --vm-version) VM_VERSION_OVERRIDE="$2"; shift 2;;
    --skip-snapshot) SKIP_SNAPSHOT=1; shift;;
    -h|--help) usage; exit 0;;
    *) err "unknown option: $1";;
  esac
done

[ "$(id -u)" -eq 0 ] || err "must run as root (use sudo)"

# --- platform detection (mirrors install.sh) ---------------------------------
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$os" in
  linux)  PLATFORM=linux;  DATA_DIR=/var/lib/leansignal-agent;;
  darwin) PLATFORM=darwin; DATA_DIR=/usr/local/var/leansignal-agent;;
  *) err "unsupported OS: $os";;
esac
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) ARCH=amd64;;
  arm64|aarch64) ARCH=arm64;;
  *) err "unsupported arch: $arch";;
esac
VM_DATA="$DATA_DIR/vm"
info "platform: ${PLATFORM}/${ARCH}"

[ -x "$BIN_DIR/leansignal-agent" ] || \
  err "no existing agent at $BIN_DIR/leansignal-agent — use install.sh for a fresh install"

# --- service control (agent | vm), per platform ------------------------------
AGENT_PLIST=/Library/LaunchDaemons/com.leansignal.agent.plist
VM_PLIST=/Library/LaunchDaemons/com.leansignal.victoria-metrics.plist

svc_stop() { # $1 = agent|vm
  if [ "$PLATFORM" = linux ]; then
    case "$1" in
      agent) systemctl stop leansignal-agent.service;;
      vm)    systemctl stop leansignal-victoria-metrics.service;;
    esac
  else
    case "$1" in
      agent) launchctl unload "$AGENT_PLIST" 2>/dev/null || true;;
      vm)    launchctl unload "$VM_PLIST" 2>/dev/null || true;;
    esac
  fi
}
svc_start() { # $1 = agent|vm ; never aborts the script (rollback must continue)
  if [ "$PLATFORM" = linux ]; then
    case "$1" in
      agent) systemctl start leansignal-agent.service || warn "failed to start agent service";;
      vm)    systemctl start leansignal-victoria-metrics.service || warn "failed to start VM service";;
    esac
  else
    case "$1" in
      agent) launchctl load -w "$AGENT_PLIST" || warn "failed to load agent daemon";;
      vm)    launchctl load -w "$VM_PLIST" || warn "failed to load VM daemon";;
    esac
  fi
}

vm_installed() {
  [ -x "$BIN_DIR/victoria-metrics" ] || return 1
  if [ "$PLATFORM" = linux ]; then
    [ -f /etc/systemd/system/leansignal-victoria-metrics.service ]
  else
    [ -f "$VM_PLIST" ]
  fi
}

wait_healthy() { # $1 = url, $2 = label ; returns 0 if healthy within ~30s
  local i
  for i in $(seq 1 30); do
    curl -fsS -o /dev/null "$1" 2>/dev/null && { info "$2 healthy"; return 0; }
    sleep 1
  done
  return 1
}

# first "X.Y.Z" (optionally -prerelease) token in a --version banner, or empty
semver_of() { printf '%s' "$1" | grep -oE '[0-9]+\.[0-9]+\.[0-9]+([-][0-9A-Za-z.]+)?' | head -1 || true; }

# --- download helpers --------------------------------------------------------
tmp="$(mktemp -d)"; trap 'rm -rf "$tmp"' EXIT
fetch() { curl -fsSL -o "$tmp/$1" "${base}/$1" || err "download failed: ${base}/$1"; }

# Fail-CLOSED integrity check: a real release always ships the checksum file, so
# a missing file or a missing line means "don't install this unverified".
verify() { # $1 = asset, $2 = checksums file (already in $tmp)
  [ -f "$tmp/$2" ] || err "checksum file $2 unavailable; refusing to install $1 unverified"
  grep -q " ${1}\$\| \./${1}\$" "$tmp/$2" || err "no pinned checksum for $1 in $2; refusing to install unverified"
  ( cd "$tmp" && { sha256sum -c <(grep -- "$1" "$2") 2>/dev/null \
      || shasum -a 256 -c <(grep -- "$1" "$2") 2>/dev/null; } ) \
    && info "checksum verified: $1" || err "checksum mismatch: $1 (aborting, nothing changed)"
}

# --- resolve target agent version --------------------------------------------
if [ "$VERSION" = latest ]; then
  info "resolving latest release..."
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | grep -m1 '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/' || true)"
  [ -n "$VERSION" ] || err "could not resolve latest version"
fi
VER_NOV="${VERSION#v}"
base="https://github.com/${REPO}/releases/download/${VERSION}"

cur_ver="$("$BIN_DIR/leansignal-agent" --version 2>/dev/null | head -1 || true)"
info "current agent: ${cur_ver:-unknown}"
info "target release: ${VERSION}"

# Exact-version skip (parsed semver equality, so 0.2.10 != 0.2.1).
cur_sem="$(semver_of "$cur_ver")"
if [ "$WITH_VM" -eq 0 ] && [ -n "$cur_sem" ] && [ "$cur_sem" = "$VER_NOV" ]; then
  info "agent already at ${VERSION}; nothing to do"
  exit 0
fi

# ============================ AGENT UPGRADE ==================================
# Uses the AGENT-ONLY archive (just the collector binary) — not the full bundle —
# so an agent upgrade never even downloads VictoriaMetrics.
agent_archive="leansignal-agent_${VER_NOV}_${PLATFORM}_${ARCH}.tar.gz"
info "downloading ${agent_archive}"
fetch "$agent_archive"
fetch "checksums.txt"
verify "$agent_archive" "checksums.txt"

tar -xzf "$tmp/$agent_archive" -C "$tmp"
newbin="$(find "$tmp" -maxdepth 2 -type f -name leansignal-agent | head -1)"
[ -n "$newbin" ] || err "agent binary not found inside $agent_archive"

backup="$BIN_DIR/leansignal-agent.prev"
info "backing up current agent -> $backup"
cp -p "$BIN_DIR/leansignal-agent" "$backup"

info "stopping the agent service (VictoriaMetrics keeps running)"
svc_stop agent
install -m 0755 "$newbin" "$BIN_DIR/leansignal-agent"
svc_start agent

if wait_healthy "http://127.0.0.1:13133/" "agent"; then
  new_ver="$("$BIN_DIR/leansignal-agent" --version 2>/dev/null | head -1 || true)"
  info "agent upgraded: ${cur_ver:-?} -> ${new_ver:-$VERSION}"
  rm -f "$backup"
else
  warn "agent did not become healthy — rolling back to the previous binary"
  svc_stop agent
  cp -p "$backup" "$BIN_DIR/leansignal-agent"
  svc_start agent
  err "agent upgrade failed and was rolled back (VictoriaMetrics data untouched)"
fi

# ============================ VM UPGRADE (opt) ===============================
if [ "$WITH_VM" -eq 1 ]; then
  if ! vm_installed; then
    warn "no local VictoriaMetrics installed here; skipping --with-vm"
  else
    # Target VM version: --vm-version, else the version this release ships (VERSIONS.txt).
    # VERSIONS.txt is optional (older releases predate it; --vm-version overrides),
    # so fetch it non-fatally and fall through to the actionable error if absent.
    vmver="$VM_VERSION_OVERRIDE"
    if [ -z "$vmver" ] && curl -fsSL -o "$tmp/VERSIONS.txt" "${base}/VERSIONS.txt" 2>/dev/null; then
      vmver="$(grep -m1 -E '^victoria-metrics=' "$tmp/VERSIONS.txt" 2>/dev/null | cut -d= -f2 | tr -d '[:space:]' || true)"
    fi
    [ -n "$vmver" ] || err "could not determine target VictoriaMetrics version; pass --vm-version X.Y.Z"

    cur_vm="$("$BIN_DIR/victoria-metrics" --version 2>&1 | head -1 || true)"
    cur_vm_sem="$(semver_of "$cur_vm")"
    info "current VM: ${cur_vm:-unknown} ; target VM: v${vmver}"
    if [ -n "$cur_vm_sem" ] && [ "$cur_vm_sem" = "$vmver" ]; then
      info "VictoriaMetrics already at v${vmver}; skipping VM upgrade"
    else
      # Instant hardlink snapshot before touching VM. This is the ONLY data-level
      # rollback point, so a failed/unconfirmed snapshot ABORTS before any change.
      snapname=""
      if [ "$SKIP_SNAPSHOT" -eq 0 ]; then
        info "creating a VictoriaMetrics snapshot before upgrading"
        snap="$(curl -fsS "http://127.0.0.1:8428/snapshot/create")" \
          || err "snapshot request failed; aborting before touching VM (use --skip-snapshot to override)"
        printf '%s' "$snap" | grep -q '"status":"ok"' \
          || err "snapshot not confirmed (response: ${snap}); aborting before touching VM"
        snapname="$(printf '%s' "$snap" | sed -E 's/.*"snapshot":"([^"]+)".*/\1/')"
        info "snapshot ok: ${snapname:-created} (under ${VM_DATA}/snapshots)"
      else
        warn "--skip-snapshot: proceeding without a pre-upgrade snapshot"
      fi

      vm_archive="victoria-metrics-${PLATFORM}-${ARCH}-v${vmver}.tar.gz"
      info "downloading ${vm_archive}"
      fetch "$vm_archive"
      fetch "bundle-checksums.txt"
      verify "$vm_archive" "bundle-checksums.txt"

      vmx="$tmp/vmx"; mkdir -p "$vmx"; tar -xzf "$tmp/$vm_archive" -C "$vmx"
      newvm="$(find "$vmx" -type f -name 'victoria-metrics*prod*' | head -1)"
      [ -n "$newvm" ] || newvm="$(find "$vmx" -type f -name 'victoria-metrics*' | head -1)"
      [ -n "$newvm" ] || err "VictoriaMetrics binary not found inside $vm_archive"

      vmbackup="$BIN_DIR/victoria-metrics.prev"
      cp -p "$BIN_DIR/victoria-metrics" "$vmbackup"
      info "stopping VictoriaMetrics (data at ${VM_DATA} is preserved)"
      svc_stop vm
      install -m 0755 "$newvm" "$BIN_DIR/victoria-metrics"
      svc_start vm

      if wait_healthy "http://127.0.0.1:8428/health" "VictoriaMetrics"; then
        info "VictoriaMetrics upgraded -> v${vmver}"
        rm -f "$vmbackup"
        # upgrade confirmed healthy — remove the pre-upgrade snapshot we created
        # so snapshots don't accumulate and pin disk under the data dir.
        [ -n "$snapname" ] && curl -fsS "http://127.0.0.1:8428/snapshot/delete?snapshot=${snapname}" >/dev/null 2>&1 \
          && info "removed pre-upgrade snapshot ${snapname}" || true
      else
        warn "VictoriaMetrics did not become healthy — rolling back"
        svc_stop vm
        cp -p "$vmbackup" "$BIN_DIR/victoria-metrics"
        svc_start vm
        [ -n "$snapname" ] && warn "kept snapshot ${snapname} under ${VM_DATA}/snapshots for manual recovery"
        err "VictoriaMetrics upgrade failed and was rolled back (data dir untouched)"
      fi
    fi
  fi
fi

info "upgrade complete. health: http://127.0.0.1:13133  vm: http://127.0.0.1:8428"
