# Install on Linux

Installs the agent and a co-located VictoriaMetrics, registered as **systemd**
services. Requires root (the script uses `sudo`). amd64 and arm64 are supported.

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
| `--version vX.Y.Z` | install a specific version (default: latest) |
| `--no-vm` | don't install the local VictoriaMetrics |
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
```

To send your own application metrics, point any OpenTelemetry SDK at the agent's
OTLP endpoint (`http://127.0.0.1:4318` for HTTP, `:4317` for gRPC).

## What it installs

| Path | |
|------|---|
| `/usr/local/bin/leansignal-agent`, `/usr/local/bin/victoria-metrics` | binaries |
| `/etc/leansignal-agent/config.yaml` | collector config |
| `/etc/leansignal-agent/agent.env` | endpoint + key (mode 0600) |
| `/var/lib/leansignal-agent/vm` | local VM data |
| `/etc/systemd/system/leansignal-agent.service`, `leansignal-victoria-metrics.service` | services |

## Manage

Two **independent** systemd units — the collector (`leansignal-agent`) and the
local store (`leansignal-victoria-metrics`). Manage each separately; restarting one
does not touch the other.

```bash
# status of both
systemctl status leansignal-agent leansignal-victoria-metrics
systemctl is-active leansignal-agent leansignal-victoria-metrics

# AGENT — start / stop / restart (VictoriaMetrics keeps running)
sudo systemctl restart leansignal-agent
sudo systemctl stop    leansignal-agent
sudo systemctl start   leansignal-agent

# VICTORIA-METRICS — start / stop / restart
sudo systemctl restart leansignal-victoria-metrics

# live logs (per service)
journalctl -u leansignal-agent -f
journalctl -u leansignal-victoria-metrics -f
```

Local store: `http://127.0.0.1:8428` · agent health: `http://127.0.0.1:13133`.

### Local VM retention

The local store keeps a **fixed 1 day (24h)** of data by design — it's a short edge
buffer (full fidelity is kept locally; only the demanded subset is forwarded to the
central dataplane). It's set to `--retentionPeriod=1d` in
`/etc/systemd/system/leansignal-victoria-metrics.service` and is not a configurable
option.

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
```
```bash
sudo systemctl restart leansignal-agent
```

Changing the **tenant** updates **three** values — the key **and** both hosts
(`-grpc` control + `-ingest` dataplane embed the tenant name). To change only the
key, edit `LEANSIGNAL_AGENT_KEY`. Or just re-run the installer with
`--agent-key` / `--tenant` (it rewrites these and keeps your config + VM data).

> If `agent.env` isn't there (an older install may have set the env differently),
> find the real location with `systemctl cat leansignal-agent` — edit the file the
> `EnvironmentFile=` line points to, or if the values are inline `Environment=` lines,
> use `sudo systemctl edit leansignal-agent`. Re-running the installer normalizes it.

## Upgrading

Upgrade just the agent — VictoriaMetrics and its data are untouched:
```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/upgrade.sh | sudo bash
```
See [Upgrading](upgrading.md) for agent-only vs VM upgrades, data safety, and rollback.

## Uninstall

Removes both binaries + both services. Keeps config + VM data unless you pass `--purge`.

**Download, then run** (clearest — `--purge` is a normal script argument):

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/uninstall.sh -o uninstall.sh
sudo bash uninstall.sh            # keep config + VM data
sudo bash uninstall.sh --purge    # also delete config + VM data
```

One-liner equivalent — `--purge` **must** come after `-s --` (that hands it to the
script; putting it on `curl` or `bash` errors with "unknown/invalid option"):

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/uninstall.sh | sudo bash -s -- --purge
```
