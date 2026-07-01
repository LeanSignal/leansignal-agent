# Configuration

The agent uses a standard OpenTelemetry Collector config file. The reference for
host installs is [`config/agent-config.example.yaml`](../config/agent-config.example.yaml);
on Kubernetes the [Helm chart](../deploy/helm/leansignal-agent) renders an
equivalent config from its values.

## Required settings

| Setting | Env var (host) | Helm value | Purpose |
|---------|----------------|------------|---------|
| Control-plane URL | `LEANSIGNAL_ENDPOINT` | `leansignal.endpoint` | gRPC target (`host:port`) of the LeanSignal API |
| Agent key | `LEANSIGNAL_AGENT_KEY` | `leansignal.agentKey.value` / `existingSecret` | per-agent auth (secret) — used both as gRPC metadata and as the dataplane bearer token |
| Dataplane URL | `LEANSIGNAL_DATAPLANE_ENDPOINT` | `dataplane.endpoint` | central remote-write target (vmauth ingest in prod) |
| Local VM URL | (default `http://127.0.0.1:8428`) | `localVM.writeEndpoint` | local full-fidelity store |

Never hard-code the agent key in the config file — always pass it via env/secret.

## Edge controller settings

The `leansignal_edge_controller` extension takes these keys (see
[`config/agent-config.example.yaml`](../config/agent-config.example.yaml)):

| Key | Default | Purpose |
|-----|---------|---------|
| `endpoint` | — | gRPC target (`host:port`, no scheme). Prod: `…-grpc.<domain>:443` |
| `agent_key` | — | per-agent auth (set from env/secret) |
| `insecure` | `false` | `true` = plaintext h2c (local dev only); `false` = TLS on 443 |
| `local_vm_query_url` | `http://127.0.0.1:8428` | **base** URL of the local VM's query API — the edit-mode query tunnel runs UI queries here; the agent appends `/api/v1/query…` itself |
| `reconnect_interval` | `5s` | backoff between reconnects |
| `ping_interval` | `30s` | heartbeat + gRPC keepalive |

**Base URLs (no path).** The endpoints above are base URLs; the config appends the
write path and the agent appends the query path. So a single value drives both — e.g.
`LEANSIGNAL_LOCAL_VM=http://localhost:8482` yields `…:8482/api/v1/write` for the
exporter and `…:8482/api/v1/query` for the tunnel. On Kubernetes the chart derives
`localVM.queryEndpoint` from `localVM.writeEndpoint` (trimming `/api/v1/write`)
unless you set it explicitly.

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

## Applying config changes

The collector config is read **at startup**, so changes to receivers/pipelines
take effect on a **restart** of the agent. (The pushed *demand list* updates live
and needs no restart — only edits to the config file/values do.) Validate first:

```bash
leansignal-agent validate --config <path-to-config>
```

| Platform | Edit | Apply |
|----------|------|-------|
| Linux (systemd) | `/etc/leansignal-agent/config.yaml` (env in `agent.env`) | `sudo systemctl restart leansignal-agent` |
| macOS (launchd) | `/usr/local/etc/leansignal-agent/config.yaml` | `sudo launchctl kickstart -k system/com.leansignal.agent` |
| Windows | `%ProgramData%\LeanSignal\Agent\config.yaml` | `Restart-Service LeanSignalAgent` |
| Kubernetes | `helm upgrade … -f values.yaml` | pod auto-rolls (the chart stamps a `checksum/config` annotation); or `kubectl -n <ns> rollout restart deploy/leansignal-agent` |
| Local dev | `config/agent-config.local.yaml` | Ctrl-C, then `make local-run` |

Changing the **endpoint or agent key** on hosts means editing `agent.env` (Linux),
the plist (macOS), or the service's registry `Environment` (Windows), then
restarting; on Kubernetes, `helm upgrade` with the new `leansignal.*` values.

## Tuning the stores

- Local VM retention/size: host installs run VictoriaMetrics with
  `--retentionPeriod=1d` (edit the service unit); on Kubernetes set
  `victoria-metrics-single.server.retentionPeriod` and `persistentVolume.size`.
- Central dataplane is remote — only the demanded subset is sent, controlled by
  the demand list from the control plane (no local setting).
