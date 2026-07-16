#!/usr/bin/env bash
# Uninstall the LeanSignal Agent + local VictoriaMetrics (Linux/macOS).
# Keeps data unless --purge is given.
set -euo pipefail

PURGE=0
[ "${1:-}" = "--purge" ] && PURGE=1

info() { printf '\033[0;36m[leansignal]\033[0m %s\n' "$*"; }
[ "$(id -u)" -eq 0 ] || { echo "must run as root (sudo)" >&2; exit 1; }

os="$(uname -s | tr '[:upper:]' '[:lower:]')"

if [ "$os" = linux ]; then
  CONF_DIR=/etc/leansignal-agent; DATA_DIR=/var/lib/leansignal-agent
  for svc in leansignal-agent leansignal-victoria-metrics leansignal-loki leansignal-tempo; do
    systemctl disable --now "${svc}.service" 2>/dev/null || true
    rm -f "/etc/systemd/system/${svc}.service"
  done
  systemctl daemon-reload 2>/dev/null || true
else
  CONF_DIR=/usr/local/etc/leansignal-agent; DATA_DIR=/usr/local/var/leansignal-agent
  for lbl in com.leansignal.agent com.leansignal.victoria-metrics; do
    launchctl unload "/Library/LaunchDaemons/${lbl}.plist" 2>/dev/null || true
    rm -f "/Library/LaunchDaemons/${lbl}.plist"
  done
fi

rm -f /usr/local/bin/leansignal-agent /usr/local/bin/victoria-metrics /usr/local/bin/loki /usr/local/bin/tempo
info "removed binaries and services"

if [ "$PURGE" -eq 1 ]; then
  rm -rf "$CONF_DIR" "$DATA_DIR"
  info "purged config and data"
else
  info "kept config ($CONF_DIR) and data ($DATA_DIR); use --purge to remove"
fi
