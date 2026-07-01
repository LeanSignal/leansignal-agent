# Install on Linux

Installs the agent and a co-located VictoriaMetrics, registered as **systemd**
services. Requires root (the script uses `sudo`). amd64 and arm64 are supported.

## Install

You only need your agent key + tenant; the gRPC and ingest hosts are derived.

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/install.sh \
  | sudo bash -s -- --agent-key YOUR_KEY --tenant YOUR_TENANT
```

> Review the script before piping it to a shell. You can also download it from a
> release bundle and run it directly. Run without flags to be prompted for the
> tenant and agent key.

### Options

| Flag | Meaning |
|------|---------|
| `--agent-key` | agent auth key (required) |
| `--tenant` | tenant name; derives `<tenant>-grpc.<domain>:443` and `…-ingest.<domain>` (required unless `--endpoint` is given) |
| `--domain` | cluster domain (default: `eu11.leansignal.io`) |
| `--endpoint` | advanced: gRPC control host `host:port`, overrides the derived one |
| `--dataplane-endpoint` | advanced: remote-write URL, overrides the derived one |
| `--version vX.Y.Z` | install a specific version (default: latest) |
| `--no-vm` | don't install the local VictoriaMetrics |
| `--from-upstream` | pull VictoriaMetrics from upstream instead of the bundle |

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

```bash
systemctl status leansignal-agent
journalctl -u leansignal-agent -f
systemctl restart leansignal-agent
```

Local store: `http://127.0.0.1:8428` · agent health: `http://127.0.0.1:13133`.

## Upgrading

Upgrade just the agent — VictoriaMetrics and its data are untouched:
```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/upgrade.sh | sudo bash
```
See [Upgrading](upgrading.md) for agent-only vs VM upgrades, data safety, and rollback.

## Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/uninstall.sh | sudo bash
# add --purge to also remove config and data
```
