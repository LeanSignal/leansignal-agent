#!/usr/bin/env bash
# Build combined LeanSignal Agent bundles (collector + VictoriaMetrics + service
# scripts + example config) for each platform, plus re-hosted VM archives.
#
# Runs AFTER goreleaser (it consumes the per-platform binaries goreleaser built
# into dist/). The resulting files in bundles/ are uploaded to the GitHub release.
#
# Usage:
#   VERSION=0.1.0 ./scripts/release/build-bundles.sh
#
# Env:
#   VERSION       agent version without leading 'v' (defaults to git describe)
#   VM_VERSION    VictoriaMetrics version (defaults to the VM_VERSION file)
#   DIST_DIR      goreleaser output dir (default: dist)
#   OUT_DIR       output dir for bundles (default: bundles)
#   STRICT_VM     if "1", fail when a VM checksum is not pinned (default: 0)
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$REPO_ROOT"

VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null | sed 's/^v//')}"
VM_VERSION="${VM_VERSION:-$(cat VM_VERSION 2>/dev/null || true)}"
DIST_DIR="${DIST_DIR:-dist}"
OUT_DIR="${OUT_DIR:-bundles}"
STRICT_VM="${STRICT_VM:-0}"
VM_CHECKSUMS="scripts/release/vm-checksums.txt"

if [ -z "$VM_VERSION" ]; then
  echo "ERROR: VM_VERSION not set and VM_VERSION file missing" >&2
  exit 1
fi

# Platforms to build bundles for: "<os>/<arch>"
PLATFORMS=("linux/amd64" "linux/arm64" "darwin/amd64" "darwin/arm64" "windows/amd64")

VM_BASE="https://github.com/VictoriaMetrics/VictoriaMetrics/releases/download/v${VM_VERSION}"

mkdir -p "$OUT_DIR" .vmcache

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}';
  else shasum -a 256 "$1" | awk '{print $1}'; fi
}

verify_vm() {
  # $1 = file path, $2 = asset filename
  local file="$1" name="$2" want got
  if [ -f "$VM_CHECKSUMS" ] && want="$(grep " ${name}\$" "$VM_CHECKSUMS" 2>/dev/null | awk '{print $1}')" && [ -n "$want" ]; then
    got="$(sha256 "$file")"
    if [ "$want" != "$got" ]; then
      echo "ERROR: checksum mismatch for $name (want $want got $got)" >&2; exit 1
    fi
    echo "  verified $name against pinned checksum" >&2
  else
    echo "  WARNING: no pinned checksum for $name (add one to $VM_CHECKSUMS)" >&2
    [ "$STRICT_VM" = "1" ] && { echo "ERROR: STRICT_VM=1 and checksum missing" >&2; exit 1; } || true
  fi
}

find_collector_bin() {
  # $1=os $2=arch ; echoes path to the goreleaser-built collector binary
  local os="$1" arch="$2" bin="leansignal-agent"
  [ "$os" = "windows" ] && bin="leansignal-agent.exe"
  # goreleaser dir layout: dist/leansignal-agent_<os>_<arch>[_variant]/<bin>
  local match
  match="$(find "$DIST_DIR" -type f -path "*leansignal-agent_${os}_${arch}*/${bin}" | head -1)"
  echo "$match"
}

download_vm() {
  # $1=os $2=arch ; echoes path to extracted VM binary, or empty if unavailable
  local os="$1" arch="$2" ext="tar.gz"
  [ "$os" = "windows" ] && ext="zip"
  local asset="victoria-metrics-${os}-${arch}-v${VM_VERSION}.${ext}"
  local url="${VM_BASE}/${asset}"
  local dl=".vmcache/${asset}"
  local extract=".vmcache/${os}-${arch}"

  if [ ! -f "$dl" ]; then
    echo "  downloading $asset" >&2
    if ! curl -fsSL -o "$dl" "$url"; then
      echo "  WARNING: VM asset not available: $url" >&2
      return 0
    fi
  fi
  verify_vm "$dl" "$asset"
  # keep a re-hosted copy of the upstream archive
  cp "$dl" "$OUT_DIR/$asset"

  rm -rf "$extract"; mkdir -p "$extract"
  if [ "$ext" = "zip" ]; then unzip -qo "$dl" -d "$extract"; else tar -xzf "$dl" -C "$extract"; fi
  # the single-node binary inside is victoria-metrics-prod (or *-prod.exe on windows)
  find "$extract" -type f \( -name 'victoria-metrics*prod*' -o -name 'victoria-metrics*' \) | head -1
}

echo "Building bundles: agent v${VERSION}, VictoriaMetrics v${VM_VERSION}"

for p in "${PLATFORMS[@]}"; do
  os="${p%/*}"; arch="${p#*/}"
  echo "== ${os}/${arch} =="

  col_bin="$(find_collector_bin "$os" "$arch")"
  if [ -z "$col_bin" ]; then
    echo "  WARNING: collector binary not found in $DIST_DIR for ${os}/${arch}; skipping" >&2
    continue
  fi

  vm_bin="$(download_vm "$os" "$arch")"

  # stage a bundle tree
  stage=".vmcache/stage-${os}-${arch}"
  rm -rf "$stage"; mkdir -p "$stage/bin" "$stage/config" "$stage/service-templates"

  if [ "$os" = "windows" ]; then
    cp "$col_bin" "$stage/bin/leansignal-agent.exe"
    [ -n "$vm_bin" ] && cp "$vm_bin" "$stage/bin/victoria-metrics.exe"
  else
    cp "$col_bin" "$stage/bin/leansignal-agent"; chmod +x "$stage/bin/leansignal-agent"
    [ -n "$vm_bin" ] && { cp "$vm_bin" "$stage/bin/victoria-metrics"; chmod +x "$stage/bin/victoria-metrics"; }
  fi

  cp config/agent-config.example.yaml "$stage/config/config.yaml"
  cp -R scripts/install/service-templates/. "$stage/service-templates/" 2>/dev/null || true
  cp LICENSE NOTICE "$stage/"
  [ -n "$vm_bin" ] || echo "NOTE: VictoriaMetrics binary not bundled for this platform; install with --from-upstream or --no-vm." > "$stage/VM-NOT-INCLUDED.txt"

  # archive
  name="leansignal-agent-bundle_${VERSION}_${os}_${arch}"
  if [ "$os" = "windows" ]; then
    (cd "$stage" && zip -qr "../../$OUT_DIR/${name}.zip" .)
    echo "  -> $OUT_DIR/${name}.zip"
  else
    tar -C "$stage" -czf "$OUT_DIR/${name}.tar.gz" .
    echo "  -> $OUT_DIR/${name}.tar.gz"
  fi
done

# checksums for everything we produced
( cd "$OUT_DIR" && \
  if command -v sha256sum >/dev/null 2>&1; then sha256sum ./* > bundle-checksums.txt; \
  else shasum -a 256 ./* > bundle-checksums.txt; fi ) || true

echo "Done. Artifacts in $OUT_DIR/:"
ls -1 "$OUT_DIR"
