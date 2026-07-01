# Install on macOS

Installs the agent and a co-located VictoriaMetrics, registered as **launchd**
daemons. Requires root (the script uses `sudo`). Apple silicon (arm64) and Intel
(amd64) are supported.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/install.sh \
  | sudo bash -s -- --agent-key YOUR_KEY --tenant YOUR_TENANT
```

The same script handles Linux and macOS; it detects the platform automatically.
See [install-linux.md](install-linux.md) for the full flag list.

## It's already collecting

The installer creates and starts the launchd daemons, so the agent is running
now. **Your Mac's host metrics — CPU, memory, disk, filesystem, network — are
collected automatically**; nothing else to configure. Verify:

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
| `/usr/local/etc/leansignal-agent/config.yaml` | collector config |
| `/usr/local/var/leansignal-agent/vm` | local VM data |
| `/usr/local/var/log/leansignal-agent/` | logs |
| `/Library/LaunchDaemons/com.leansignal.agent.plist`, `com.leansignal.victoria-metrics.plist` | services |

## Manage

Two **independent** LaunchDaemons — the collector (`com.leansignal.agent`) and the
local store (`com.leansignal.victoria-metrics`). Manage each separately; stopping
one does not affect the other.

```bash
# STATUS — both (a numeric PID in the first column = running)
sudo launchctl list | grep leansignal

# RESTART either one (one-liner; the other is unaffected)
sudo launchctl kickstart -k system/com.leansignal.agent
sudo launchctl kickstart -k system/com.leansignal.victoria-metrics

# STOP / START (unload = stop, load = start)
sudo launchctl unload /Library/LaunchDaemons/com.leansignal.agent.plist
sudo launchctl load   -w /Library/LaunchDaemons/com.leansignal.agent.plist
# …same with com.leansignal.victoria-metrics.plist for the store

# LOGS — one file per service
tail -f /usr/local/var/log/leansignal-agent/agent.log
tail -f /usr/local/var/log/leansignal-agent/victoria-metrics.log
```

> macOS system daemons live in the `system/` domain and need `sudo`; the labels are
> `com.leansignal.agent` and `com.leansignal.victoria-metrics`.

Local store: `http://127.0.0.1:8428` · agent health: `http://127.0.0.1:13133`.

### Local VM retention

The local store keeps a **fixed 1 day (24h)** of data by design — it's a short edge
buffer (full fidelity is kept locally; only the demanded subset is forwarded to the
central dataplane). It's set to `--retentionPeriod=1d` in
`/Library/LaunchDaemons/com.leansignal.victoria-metrics.plist` and is not a
configurable option.

> Note: macOS binaries from a release are not notarized; Gatekeeper may require
> approval the first time. Bundles installed via the script run as root daemons.

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
