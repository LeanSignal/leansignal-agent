# Install on Linux

Installs the agent and a co-located VictoriaMetrics, registered as **systemd**
services. Requires root (the script uses `sudo`). amd64 and arm64 are supported.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/install.sh \
  | sudo bash -s -- \
    --agent-key YOUR_KEY \
    --endpoint api.leansignal.com:443 \
    --dataplane-endpoint https://dataplane.example.com/api/v1/write
```

> Review the script before piping it to a shell. You can also download it from a
> release bundle and run it directly.

### Options

| Flag | Meaning |
|------|---------|
| `--agent-key` | agent auth key (required) |
| `--endpoint` | LeanSignal gRPC endpoint (host:port) (required) |
| `--dataplane-endpoint` | central remote-write URL (required) |
| `--version vX.Y.Z` | install a specific version (default: latest) |
| `--no-vm` | don't install the local VictoriaMetrics |
| `--from-upstream` | pull VictoriaMetrics from upstream instead of the bundle |

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

## Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/uninstall.sh | sudo bash
# add --purge to also remove config and data
```
