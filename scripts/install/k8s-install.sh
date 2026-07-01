#!/usr/bin/env bash
# Install the LeanSignal Agent on Kubernetes via Helm (OCI chart).
#
# You only need your agent key + tenant; the gRPC control host and the ingest host
# are derived from the tenant and domain (default eu11.leansignal.io):
#   ./k8s-install.sh --agent-key KEY --tenant TENANT
#
# Advanced: override with --endpoint / --dataplane-endpoint, or --domain.
set -euo pipefail

CHART="${CHART:-oci://ghcr.io/leansignal/charts/leansignal-agent}"
RELEASE="${RELEASE:-leansignal-agent}"
NAMESPACE="${NAMESPACE:-leansignal}"
VERSION="${CHART_VERSION:-}"
AGENT_KEY=""; TENANT=""; DOMAIN="${LEANSIGNAL_DOMAIN:-eu11.leansignal.io}"
ENDPOINT=""; DATAPLANE_ENDPOINT=""; ENABLE_VM=1

usage() { sed -n '2,9p' "$0"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --agent-key) AGENT_KEY="$2"; shift 2;;
    --tenant) TENANT="$2"; shift 2;;
    --domain) DOMAIN="$2"; shift 2;;
    --endpoint) ENDPOINT="$2"; shift 2;;
    --dataplane-endpoint) DATAPLANE_ENDPOINT="$2"; shift 2;;
    --namespace) NAMESPACE="$2"; shift 2;;
    --chart-version) VERSION="$2"; shift 2;;
    --no-vm) ENABLE_VM=0; shift;;
    -h|--help) usage; exit 0;;
    *) echo "unknown option: $1" >&2; exit 1;;
  esac
done

command -v helm >/dev/null 2>&1 || { echo "helm not found" >&2; exit 1; }
[ -n "$AGENT_KEY" ] || { echo "--agent-key required" >&2; exit 1; }
[ -n "$TENANT" ] || [ -n "$ENDPOINT" ] || { echo "--tenant required (or --endpoint)" >&2; exit 1; }

args=(upgrade --install "$RELEASE" "$CHART"
  --namespace "$NAMESPACE" --create-namespace
  --set "leansignal.agentKey.value=${AGENT_KEY}"
  --set "victoria-metrics-single.enabled=$([ "$ENABLE_VM" -eq 1 ] && echo true || echo false)")

# Pass the tenant (chart derives the control + ingest hosts), or explicit overrides.
[ -n "$TENANT" ]             && args+=(--set "leansignal.tenant=${TENANT}" --set "leansignal.domain=${DOMAIN}")
[ -n "$ENDPOINT" ]           && args+=(--set "leansignal.endpoint=${ENDPOINT}")
[ -n "$DATAPLANE_ENDPOINT" ] && args+=(--set "dataplane.endpoint=${DATAPLANE_ENDPOINT}")
[ -n "$VERSION" ]            && args+=(--version "$VERSION")

echo "+ helm ${args[*]}"
helm "${args[@]}"
