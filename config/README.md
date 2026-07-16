# Configuration

The LeanSignal Agent is an OpenTelemetry Collector distribution. It is configured
with a standard collector config file.

- [`agent-config.example.yaml`](agent-config.example.yaml) — the reference
  configuration used for **host installs** (Linux/macOS/Windows). The install
  scripts copy it to the platform config directory and the service runs the
  agent with `--config` pointing at it.

For **Kubernetes**, do not use this file directly — the
[Helm chart](../deploy/helm/leansignal-agent) renders an equivalent config from
its `values.yaml`.

## Config directory per platform

| Platform | Config path |
|----------|-------------|
| Linux    | `/etc/leansignal-agent/config.yaml` |
| macOS    | `/usr/local/etc/leansignal-agent/config.yaml` |
| Windows  | `%ProgramData%\LeanSignal\Agent\config.yaml` |

## Required environment

These are provided by the service unit / installer, never hard-coded in the file:

| Variable | Purpose |
|----------|---------|
| `LEANSIGNAL_ENDPOINT` | gRPC control-plane URL (`api.leansignal.com:443`) |
| `LEANSIGNAL_AGENT_KEY` | Per-agent auth key (secret) |
| `LEANSIGNAL_DATAPLANE_ENDPOINT` | Prometheus remote-write URL of the central metrics store |
| `LEANSIGNAL_LOKI_ENDPOINT` | Tenant logs-ingest base URL (demanded log streams push here) |
| `LEANSIGNAL_TEMPO_ENDPOINT` | Tenant traces-ingest base URL (demanded spans push here) |

Each signal has a co-located local store installed next to the agent, defaulting
to loopback: VictoriaMetrics for metrics (`http://127.0.0.1:8428/api/v1/write`),
Loki for logs, and Tempo for traces.

See [`../docs/configuration.md`](../docs/configuration.md) for the full reference,
including the demand/index model and how the per-signal pipelines relate.
