#!/usr/bin/env bash
# Install the LeanSignal Agent on Kubernetes via Helm (OCI chart).
#
#   ./k8s-install.sh --agent-key KEY \
#     --endpoint wss://api.leansignal.com/api/v1/agents/ws/ \
#     --dataplane-endpoint https://dataplane.example.com/api/v1/write
set -euo pipefail

CHART="${CHART:-oci://ghcr.io/leansignal/charts/leansignal-agent}"
RELEASE="${RELEASE:-leansignal-agent}"
NAMESPACE="${NAMESPACE:-leansignal}"
VERSION="${CHART_VERSION:-}"
AGENT_KEY=""; ENDPOINT=""; DATAPLANE_ENDPOINT=""; ENABLE_VM=1

usage() { sed -n '2,8p' "$0"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --agent-key) AGENT_KEY="$2"; shift 2;;
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
[ -n "$ENDPOINT" ] || { echo "--endpoint required" >&2; exit 1; }
[ -n "$DATAPLANE_ENDPOINT" ] || { echo "--dataplane-endpoint required" >&2; exit 1; }

args=(upgrade --install "$RELEASE" "$CHART"
  --namespace "$NAMESPACE" --create-namespace
  --set "leansignal.endpoint=${ENDPOINT}"
  --set "leansignal.agentKey.value=${AGENT_KEY}"
  --set "dataplane.endpoint=${DATAPLANE_ENDPOINT}"
  --set "victoria-metrics-single.enabled=$([ "$ENABLE_VM" -eq 1 ] && echo true || echo false)")
[ -n "$VERSION" ] && args+=(--version "$VERSION")

echo "+ helm ${args[*]}"
helm "${args[@]}"
