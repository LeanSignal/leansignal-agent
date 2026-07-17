# Install on Linux

Installs the agent, a co-located VictoriaMetrics (local metrics store), a
co-located Loki (local log store) and a co-located Tempo (local trace store),
registered as **systemd** services. Requires
root (the script uses `sudo`). amd64 and arm64 are supported.

## Install

You need your agent key, an agent name, and the tenant; the gRPC and ingest
hosts are derived.

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/install.sh \
  | sudo bash -s -- --agent-key YOUR_KEY --agent-name this-host --tenant YOUR_TENANT
```

> Review the script before piping it to a shell. You can also download it from a
> release bundle and run it directly. Run without flags to be prompted for the
> tenant, agent key, and agent name.

### Options

| Flag | Meaning |
|------|---------|
| `--agent-key` | agent auth key (required, both modes) |
| `--agent-name` | name identifying this agent/host; becomes the `leansignal_agent_name` label on every metric (required, both modes) |
| `--central-url` | install in **edge** mode: forward OTLP to this central agent (`host:port`, plaintext). Also via `CENTRAL_AGENT_GRPC_URL`. No local VM; `--tenant` not needed |
| `--tenant` | tenant name; derives `<tenant>-grpc.<domain>:443` and `…-ingest.<domain>` (required for **central** mode unless `--endpoint` is given) |
| `--domain` | cluster domain (default: `eu11.leansignal.io`) |
| `--endpoint` | advanced: gRPC control host `host:port`, overrides the derived one |
| `--dataplane-endpoint` | advanced: remote-write URL, overrides the derived one |
| `--loki-endpoint` | advanced: tenant logs-ingest base URL, overrides the derived one |
| `--tempo-endpoint` | advanced: tenant traces-ingest base URL, overrides the derived one |
| `--version vX.Y.Z` | install a specific version (default: latest) |
| `--bundle FILE` | install from a local bundle tar.gz (e.g. built by `scripts/release/build-bundles.sh`) instead of downloading a release |
| `--no-vm` | don't install the local VictoriaMetrics |
| `--no-loki` | don't install the local Loki (log store) |
| `--loki-version X.Y.Z` | override the pinned Loki version |
| `--no-tempo` | don't install the local Tempo (trace store) |
| `--tempo-version X.Y.Z` | override the pinned Tempo version |
| `--from-upstream` | pull VictoriaMetrics from upstream instead of the bundle |

### Edge mode

To install a lightweight **edge** agent that forwards OTLP to a central agent
(no local VM, tracker, demand filter, or control channel), pass the central
agent's OTLP endpoint:

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/install.sh \
  | sudo bash -s -- --agent-key YOUR_KEY --agent-name this-host --central-url central.internal:4317
```

## It's already collecting

The installer creates and starts the systemd services, so the agent is running
now. **Host metrics — CPU, memory, disk, filesystem, network — are collected
automatically**; there's nothing else to configure. Verify:

```bash
curl -sf http://127.0.0.1:13133/ && echo " agent healthy"          # health check
curl -s http://127.0.0.1:8428/api/v1/label/__name__/values         # metric names in the local store
curl -sf http://127.0.0.1:3100/ready && echo " local loki ready"   # local log store
curl -sf http://127.0.0.1:3200/ready && echo " local tempo ready"  # local trace store
```

To send your own application metrics, **logs and traces**, point any
OpenTelemetry SDK at the agent's OTLP endpoint (`http://127.0.0.1:4318` for
HTTP, `:4317` for gRPC). Promtail/Alloy-style log shippers can push to the
agent's Loki receiver (`:3500` HTTP, `:3600` gRPC). All logs land in the local
Loki and all traces in the local Tempo; **nothing is forwarded to LeanSignal
until a dashboard demands it** (same demand model as metrics).

## What it installs

| Path | |
|------|---|
| `/usr/local/bin/leansignal-agent`, `/usr/local/bin/victoria-metrics`, `/usr/local/bin/loki`, `/usr/local/bin/tempo` | binaries |
| `/etc/leansignal-agent/config.yaml` | collector config |
| `/etc/leansignal-agent/loki.yaml` | local Loki config |
| `/etc/leansignal-agent/tempo.yaml` | local Tempo config |
| `/etc/leansignal-agent/agent.env` | endpoints + key (mode 0600) |
| `/var/lib/leansignal-agent/vm` | local VM data |
| `/var/lib/leansignal-agent/loki` | local Loki data |
| `/var/lib/leansignal-agent/tempo` | local Tempo data |
| `/etc/systemd/system/leansignal-agent.service`, `leansignal-victoria-metrics.service`, `leansignal-loki.service`, `leansignal-tempo.service` | services |

## Manage

Four **independent** systemd units — the collector (`leansignal-agent`), the
local metrics store (`leansignal-victoria-metrics`), the local log store
(`leansignal-loki`) and the local trace store (`leansignal-tempo`). Manage each
separately; restarting one does not touch the others.

```bash
# status of all four
systemctl status leansignal-agent leansignal-victoria-metrics leansignal-loki leansignal-tempo
systemctl is-active leansignal-agent leansignal-victoria-metrics leansignal-loki leansignal-tempo

# AGENT — start / stop / restart (VictoriaMetrics + Loki + Tempo keep running)
sudo systemctl restart leansignal-agent
sudo systemctl stop    leansignal-agent
sudo systemctl start   leansignal-agent

# VICTORIA-METRICS / LOKI / TEMPO — start / stop / restart
sudo systemctl restart leansignal-victoria-metrics
sudo systemctl restart leansignal-loki
sudo systemctl restart leansignal-tempo

# live logs (per service)
journalctl -u leansignal-agent -f
journalctl -u leansignal-victoria-metrics -f
journalctl -u leansignal-loki -f
journalctl -u leansignal-tempo -f
```

Local metrics store: `http://127.0.0.1:8428` · local log store:
`http://127.0.0.1:3100` · local trace store: `http://127.0.0.1:3200` · agent
health: `http://127.0.0.1:13133`.

### Local VM retention

The local store keeps a **fixed 1 day (24h)** of data by design — it's a short edge
buffer (full fidelity is kept locally; only the demanded subset is forwarded to the
central dataplane). It's set to `--retentionPeriod=1d` in
`/etc/systemd/system/leansignal-victoria-metrics.service` and is not a configurable
option.

### Local log window

The local Loki keeps a **~1 hour** window of logs by design — queries cannot see
past `max_query_lookback: 1h`, ingest rejects samples older than 1h, and the
compactor physically deletes chunks after `retention_period: 2h` (Loki deletion
is chunk-granular, so the disk bound is ~2× the logical window). Configured in
`/etc/leansignal-agent/loki.yaml`. Only log streams explicitly demanded by a
dashboard are forwarded to LeanSignal.

### Local trace window

The local Tempo keeps an **approximately 1 hour** window of traces —
`block_retention: 1h` in `/etc/leansignal-agent/tempo.yaml`. Unlike the local
Loki there is no exact query-lookback bound (Tempo deletion is
compaction-driven and block-granular), so queries may see slightly more than an
hour. Its OTLP receiver binds `127.0.0.1:4328` (the agent collector owns
4317/4318). Only traces of resources explicitly demanded by a dashboard are
forwarded to LeanSignal.

### Change the agent key or tenant

Connection details live in **`/etc/leansignal-agent/agent.env`**. Edit it, then
restart only the agent (VictoriaMetrics + its data are untouched):

```bash
sudo nano /etc/leansignal-agent/agent.env
```
```ini
LEANSIGNAL_ENDPOINT=<tenant>-grpc.eu11.leansignal.io:443
LEANSIGNAL_AGENT_KEY=<key>
LEANSIGNAL_DATAPLANE_ENDPOINT=https://<tenant>-ingest.eu11.leansignal.io/api/v1/write
LEANSIGNAL_LOKI_ENDPOINT=https://<tenant>-ingest.eu11.leansignal.io
LEANSIGNAL_TEMPO_ENDPOINT=https://<tenant>-ingest.eu11.leansignal.io
```
```bash
sudo systemctl restart leansignal-agent
```

Changing the **tenant** updates **five** values — the key **and** the hosts
(`-grpc` control + `-ingest` dataplane/logs/traces embed the tenant name). To change only the
key, edit `LEANSIGNAL_AGENT_KEY`. Or just re-run the installer with
`--agent-key` / `--tenant` (it rewrites these and keeps your config + VM data).

> If `agent.env` isn't there (an older install may have set the env differently),
> find the real location with `systemctl cat leansignal-agent` — edit the file the
> `EnvironmentFile=` line points to, or if the values are inline `Environment=` lines,
> use `sudo systemctl edit leansignal-agent`. Re-running the installer normalizes it.

## Upgrading

Upgrade just the agent — the local stores (VictoriaMetrics, Loki, Tempo) and their data are untouched:
```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/upgrade.sh | sudo bash
```
See [Upgrading](upgrading.md) for agent-only vs VM upgrades, data safety, and rollback.

## Uninstall

Removes all four binaries + services (agent, VictoriaMetrics, Loki, Tempo), any `.prev` rollback backups from an interrupted upgrade, and the services' residual systemd state. Keeps config + local store data unless you pass `--purge`.

**Download, then run** (clearest — `--purge` is a normal script argument):

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/uninstall.sh -o uninstall.sh
sudo bash uninstall.sh            # keep config + local store data
sudo bash uninstall.sh --purge    # also delete config + local store data
```

One-liner equivalent — `--purge` **must** come after `-s --` (that hands it to the
script; putting it on `curl` or `bash` errors with "unknown/invalid option"):

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/uninstall.sh | sudo bash -s -- --purge
```
