#!/usr/bin/env bash
# Install the LeanSignal Agent on Kubernetes via Helm (OCI chart).
#
# You need your agent key, an agent name, and the tenant; the gRPC control host
# and the ingest host are derived from the tenant and domain (default
# eu11.leansignal.io):
#   ./k8s-install.sh --agent-key KEY --agent-name NAME --tenant TENANT
#
# EDGE mode (forward OTLP to a central agent; no local VM, no tenant needed):
#   ./k8s-install.sh --agent-key KEY --agent-name NAME --central-url HOST:PORT
#
# Advanced: override with --endpoint / --dataplane-endpoint, or --domain.
set -euo pipefail

CHART="${CHART:-oci://ghcr.io/leansignal/charts/leansignal-agent}"
RELEASE="${RELEASE:-leansignal-agent}"
NAMESPACE="${NAMESPACE:-leansignal}"
VERSION="${CHART_VERSION:-}"
AGENT_KEY=""; AGENT_NAME=""; TENANT=""; DOMAIN="${LEANSIGNAL_DOMAIN:-eu11.leansignal.io}"
ENDPOINT=""; DATAPLANE_ENDPOINT=""; ENABLE_VM=1
CENTRAL_URL="${CENTRAL_AGENT_GRPC_URL:-}"

usage() { sed -n '2,12p' "$0"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --agent-key) AGENT_KEY="$2"; shift 2;;
    --agent-name) AGENT_NAME="$2"; shift 2;;
    --central-url) CENTRAL_URL="$2"; shift 2;;
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
[ -n "$AGENT_NAME" ] || { echo "--agent-name required" >&2; exit 1; }

# EDGE mode: forward OTLP to a central agent; no local VM, no tenant needed.
if [ -n "$CENTRAL_URL" ]; then MODE=edge; ENABLE_VM=0; else MODE=central; fi
[ "$MODE" = edge ] || [ -n "$TENANT" ] || [ -n "$ENDPOINT" ] || { echo "--tenant required (or --endpoint), or use --central-url for edge mode" >&2; exit 1; }

args=(upgrade --install "$RELEASE" "$CHART"
  --namespace "$NAMESPACE" --create-namespace
  --set "leansignal.agentKey.value=${AGENT_KEY}"
  --set "leansignal.agentName=${AGENT_NAME}"
  --set "victoria-metrics-single.enabled=$([ "$ENABLE_VM" -eq 1 ] && echo true || echo false)")

if [ "$MODE" = edge ]; then
  args+=(--set "leansignal.mode=edge" --set "leansignal.centralAgentGrpcUrl=${CENTRAL_URL}")
fi
# Pass the tenant (chart derives the control + ingest hosts), or explicit overrides.
[ -n "$TENANT" ]             && args+=(--set "leansignal.tenant=${TENANT}" --set "leansignal.domain=${DOMAIN}")
[ -n "$ENDPOINT" ]           && args+=(--set "leansignal.endpoint=${ENDPOINT}")
[ -n "$DATAPLANE_ENDPOINT" ] && args+=(--set "dataplane.endpoint=${DATAPLANE_ENDPOINT}")
[ -n "$VERSION" ]            && args+=(--version "$VERSION")

echo "+ helm ${args[*]}"
helm "${args[@]}"
