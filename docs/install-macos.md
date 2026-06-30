# Install on macOS

Installs the agent and a co-located VictoriaMetrics, registered as **launchd**
daemons. Requires root (the script uses `sudo`). Apple silicon (arm64) and Intel
(amd64) are supported.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/install.sh \
  | sudo bash -s -- \
    --agent-key YOUR_KEY \
    --endpoint api.leansignal.com:443 \
    --dataplane-endpoint https://dataplane.example.com/api/v1/write
```

The same script handles Linux and macOS; it detects the platform automatically.
See [install-linux.md](install-linux.md) for the full flag list.

## What it installs

| Path | |
|------|---|
| `/usr/local/bin/leansignal-agent`, `/usr/local/bin/victoria-metrics` | binaries |
| `/usr/local/etc/leansignal-agent/config.yaml` | collector config |
| `/usr/local/var/leansignal-agent/vm` | local VM data |
| `/usr/local/var/log/leansignal-agent/` | logs |
| `/Library/LaunchDaemons/com.leansignal.agent.plist`, `com.leansignal.victoria-metrics.plist` | services |

## Manage

```bash
sudo launchctl list | grep leansignal
sudo launchctl unload /Library/LaunchDaemons/com.leansignal.agent.plist
sudo launchctl load -w /Library/LaunchDaemons/com.leansignal.agent.plist
tail -f /usr/local/var/log/leansignal-agent/agent.log
```

> Note: macOS binaries from a release are not notarized; Gatekeeper may require
> approval the first time. Bundles installed via the script run as root daemons.

## Uninstall

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/uninstall.sh | sudo bash
# add --purge to also remove config and data
```
