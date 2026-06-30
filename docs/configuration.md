# Configuration

The agent uses a standard OpenTelemetry Collector config file. The reference for
host installs is [`config/agent-config.example.yaml`](../config/agent-config.example.yaml);
on Kubernetes the [Helm chart](../deploy/helm/leansignal-agent) renders an
equivalent config from its values.

## Required settings

| Setting | Env var (host) | Helm value | Purpose |
|---------|----------------|------------|---------|
| Control-plane URL | `LEANSIGNAL_ENDPOINT` | `leansignal.endpoint` | WebSocket to the LeanSignal API |
| Agent key | `LEANSIGNAL_AGENT_KEY` | `leansignal.agentKey.value` / `existingSecret` | per-agent auth (secret) |
| Dataplane URL | `LEANSIGNAL_DATAPLANE_ENDPOINT` | `dataplane.endpoint` | central remote-write target |
| Local VM URL | (default `http://127.0.0.1:8428/api/v1/write`) | `localVM.writeEndpoint` | local full-fidelity store |

Never hard-code the agent key in the config file — always pass it via env/secret.

## Config locations (host installs)

| Platform | Path |
|----------|------|
| Linux | `/etc/leansignal-agent/config.yaml` (env in `agent.env`) |
| macOS | `/usr/local/etc/leansignal-agent/config.yaml` |
| Windows | `%ProgramData%\LeanSignal\Agent\config.yaml` |

## Pipelines

- `metrics/all` — receivers → `leansignalmetrics_tracker` → local VM **and** a
  fan-out to the filtered pipeline.
- `metrics/filtered` — `leansignal_demand_filter` → central dataplane.

## Receivers

Host installs default to `otlp` (gRPC `4317`, HTTP `4318`) + `hostmetrics`.
On Kubernetes the chart enables `otlp`, `k8s_cluster`, and `kubeletstats`
(toggle via `receivers.*` values). Because this is a full Collector Contrib
build, you can add any upstream receiver/processor/exporter by editing the config.

## Tuning the stores

- Local VM retention/size: host installs run VictoriaMetrics with
  `--retentionPeriod=1d` (edit the service unit); on Kubernetes set
  `victoria-metrics-single.server.retentionPeriod` and `persistentVolume.size`.
- Central dataplane is remote — only the demanded subset is sent, controlled by
  the demand list from the control plane (no local setting).
