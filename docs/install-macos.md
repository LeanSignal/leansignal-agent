# Install on macOS

Installs the agent and all three co-located local stores — **VictoriaMetrics**
(metrics), **Loki** (logs) and **Tempo** (traces) — registered as **launchd**
daemons. Requires root (the script uses `sudo`). Apple silicon (arm64) and Intel
(amd64) are supported.

> macOS is a **full-feature** platform: the agent collects metrics, logs and
> traces, keeps everything locally at full fidelity for a short window, and
> forwards only what LeanSignal demands — exactly as on Linux. Skip a store with
> `--no-loki` / `--no-tempo` / `--no-vm` if you don't want it.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/install.sh \
  | sudo bash -s -- --agent-key YOUR_KEY --agent-name this-host --tenant YOUR_TENANT
```

The same script handles Linux and macOS; it detects the platform automatically.
See [install-linux.md](install-linux.md) for the full flag list.

## It's already collecting

The installer creates and starts the launchd daemons, so the agent is running
now. **Your Mac's host metrics — CPU, memory, disk, filesystem, network — are
collected automatically**; nothing else to configure.

To send your own telemetry, point any OpenTelemetry SDK at the agent's OTLP
endpoint (`http://127.0.0.1:4318` for HTTP, `:4317` for gRPC). Log shippers that
speak the Loki push API can use `:3500` (HTTP) or `:3600` (gRPC).

## Checking it works

Everything below is loopback-only, so these run on the Mac itself.

### Is the agent up?

```bash
curl -sf http://127.0.0.1:13133/ && echo " agent healthy"   # health check
sudo launchctl list | grep leansignal                       # numeric PID = running
tail -f /usr/local/var/log/leansignal-agent/agent.log       # agent's own log
```

The agent **self-monitors**: its own metrics, logs and traces are pushed through
its own pipelines under `service.name=leansignal-agent`, so it is its own first
test signal — the queries below find data even before you send anything.

### Metrics — local store on `:8428`

```bash
# what metric names exist locally
curl -s 'http://127.0.0.1:8428/api/v1/label/__name__/values' | head -c 500

# your Mac's host metrics are flowing
curl -s --get 'http://127.0.0.1:8428/api/v1/query' \
  --data-urlencode 'query=system_cpu_load_average_1m'

# the control-channel connection is up (1 = connected to LeanSignal)
curl -s --get 'http://127.0.0.1:8428/api/v1/query' \
  --data-urlencode 'query=leansignal_edgecontroller_connection_up'

# every series carries this host's identity labels
curl -s --get 'http://127.0.0.1:8428/api/v1/query' \
  --data-urlencode 'query=up{leansignal_mode="central"}'
```

### Logs — local store on `:3100`

Installed by default (skipped only if you passed `--no-loki`).

```bash
curl -s http://127.0.0.1:3100/ready                            # expect: ready

# which streams exist, and their service names
curl -s 'http://127.0.0.1:3100/loki/api/v1/labels'
curl -s 'http://127.0.0.1:3100/loki/api/v1/label/service_name/values'

# the agent's own logs (always present — good smoke test)
curl -s --get 'http://127.0.0.1:3100/loki/api/v1/query_range' \
  --data-urlencode 'query={service_name="leansignal-agent"}' \
  --data-urlencode 'limit=5'
```

> Remember the local window is **1 hour** and exact (`max_query_lookback: 1h`) —
> a query over a longer range returns nothing older than that, by design.

### Traces — local store on `:3200`

Installed by default (skipped only if you passed `--no-tempo`).

```bash
curl -s http://127.0.0.1:3200/ready                            # expect: ready

# recent traces (TraceQL; {} matches everything)
curl -s --get 'http://127.0.0.1:3200/api/search' \
  --data-urlencode 'q={}' --data-urlencode 'limit=5'

# search by a resource attribute
curl -s --get 'http://127.0.0.1:3200/api/search' \
  --data-urlencode 'tags=service.name=my-service' --data-urlencode 'limit=5'

# one trace by id (works the moment it is ingested)
curl -s 'http://127.0.0.1:3200/api/traces/<trace-id>'
```

> `/api/search/tags` (the tag-*name* index) stays empty until Tempo flushes its
> first blocks, so an empty list there shortly after install is expected — search
> and trace-by-id already work against the in-memory ingester.

> Unlike metrics and logs, the agent emits **very few spans of its own**, so an
> empty result here usually means nothing has sent traces yet rather than a
> broken store. Point an instrumented app at `:4318` and re-check.

### What is actually being forwarded to LeanSignal?

The local stores hold **everything**; only *demanded* telemetry leaves the host.
Each filter logs its verdict per batch, so the agent log tells you the split:

```bash
grep 'demand filter' /usr/local/var/log/leansignal-agent/agent.log | tail -20
```

You'll see lines like `demand filter: batch filtered` with `received` / `allowed`
/ `dropped` counts (and `log demand filter:` / `trace demand filter:` for the
other two signals). **`allowed=0` on a fresh agent is normal and correct** — the
filters are fail-closed, so nothing is forwarded until a dashboard or alert in
LeanSignal demands it. Metrics keep landing in the local store either way.

## How logs and traces are stored

The installer sets up **all three** local stores on macOS, so there is nothing
extra to do:

| Signal | Local store | Port | Local window |
|---|---|---|---|
| metrics | VictoriaMetrics | `8428` | 1 day (exact) |
| logs | Loki | `3100` | 1 hour (exact) |
| traces | Tempo | `3200` (OTLP ingest `4328`) | ~1 hour (approximate) |

Everything the agent sees is written to these stores at full fidelity; only what
LeanSignal **demands** is forwarded to your tenant. That local copy is what makes
discovery work — you browse what the agent is actually seeing and *then* choose
what to demand.

Tempo's OTLP receiver binds **4328** rather than the usual 4317/4318, because the
agent collector itself owns those ports.

If you installed with `--no-loki` or `--no-tempo`, the collector config still
contains the logs/traces pipelines (it is identical on every platform), so the
agent will log repeated connection-refused errors for the missing store and the
`leansignal-aloki` / `leansignal-atempo` scrape jobs will report `up=0`. That is
harmless — telemetry still reaches your tenant, since the tenant path is a
separate pipeline. To silence it, remove `otlphttp/loki_local` /
`otlphttp/tempo_local` from their pipelines' `exporters:` lists in
`/usr/local/etc/leansignal-agent/config.yaml` and drop the matching scrape jobs.

## What it installs

| Path | |
|------|---|
| `/usr/local/bin/leansignal-agent`, `/usr/local/bin/victoria-metrics`, `/usr/local/bin/loki`, `/usr/local/bin/tempo` | binaries |
| `/usr/local/etc/leansignal-agent/config.yaml` | collector config |
| `/usr/local/etc/leansignal-agent/loki.yaml` | local Loki config |
| `/usr/local/etc/leansignal-agent/tempo.yaml` | local Tempo config |
| `/usr/local/var/leansignal-agent/vm` | local VM data |
| `/usr/local/var/leansignal-agent/loki` | local Loki data |
| `/usr/local/var/leansignal-agent/tempo` | local Tempo data |
| `/usr/local/var/log/leansignal-agent/` | logs, one file per service |
| `/Library/LaunchDaemons/com.leansignal.agent.plist`, `com.leansignal.victoria-metrics.plist`, `com.leansignal.loki.plist`, `com.leansignal.tempo.plist` | services |

A store you skipped with `--no-vm` / `--no-loki` / `--no-tempo` contributes none
of its entries.

## Manage

Four **independent** LaunchDaemons — the collector (`com.leansignal.agent`), the
local metrics store (`com.leansignal.victoria-metrics`), the local log store
(`com.leansignal.loki`) and the local trace store (`com.leansignal.tempo`).
Manage each separately; restarting one does not touch the others.

```bash
# STATUS — all four (a numeric PID in the first column = running)
sudo launchctl list | grep leansignal

# AGENT — restart (VictoriaMetrics + Loki + Tempo keep running)
sudo launchctl kickstart -k system/com.leansignal.agent

# VICTORIA-METRICS / LOKI / TEMPO — restart
sudo launchctl kickstart -k system/com.leansignal.victoria-metrics
sudo launchctl kickstart -k system/com.leansignal.loki
sudo launchctl kickstart -k system/com.leansignal.tempo

# STOP / START any one (unload = stop, load = start)
sudo launchctl unload /Library/LaunchDaemons/com.leansignal.agent.plist
sudo launchctl load   -w /Library/LaunchDaemons/com.leansignal.agent.plist
# …same for com.leansignal.victoria-metrics.plist / .loki.plist / .tempo.plist

# LIVE LOGS (per service)
tail -f /usr/local/var/log/leansignal-agent/agent.log
tail -f /usr/local/var/log/leansignal-agent/victoria-metrics.log
tail -f /usr/local/var/log/leansignal-agent/loki.log
tail -f /usr/local/var/log/leansignal-agent/tempo.log
```

> macOS system daemons live in the `system/` domain and need `sudo`; the labels are
> `com.leansignal.agent`, `com.leansignal.victoria-metrics`,
> `com.leansignal.loki` and `com.leansignal.tempo`.

Local metrics store: `http://127.0.0.1:8428` · local log store:
`http://127.0.0.1:3100` · local trace store: `http://127.0.0.1:3200` · agent
health: `http://127.0.0.1:13133`.

### Configuration files

Edit the file, then restart **only** that service — the others keep running and
no data is lost.

| Service | Config | Restart with |
|---|---|---|
| agent (collector) | `/usr/local/etc/leansignal-agent/config.yaml` | `sudo launchctl kickstart -k system/com.leansignal.agent` |
| VictoriaMetrics (metrics) | flags in `/Library/LaunchDaemons/com.leansignal.victoria-metrics.plist` | `sudo launchctl kickstart -k system/com.leansignal.victoria-metrics` |
| Loki (logs) | `/usr/local/etc/leansignal-agent/loki.yaml` | `sudo launchctl kickstart -k system/com.leansignal.loki` |
| Tempo (traces) | `/usr/local/etc/leansignal-agent/tempo.yaml` | `sudo launchctl kickstart -k system/com.leansignal.tempo` |

Connection details (tenant, agent key) are **not** in a config file on macOS —
they live in the agent plist's `EnvironmentVariables`, see
[Change the agent key or tenant](#change-the-agent-key-or-tenant) below.
Re-running the installer never clobbers an existing config; it writes
`config.yaml.new` / `loki.yaml.new` / `tempo.yaml.new` beside it instead.

### Local retention windows

Each local store is a short, full-fidelity edge buffer — everything is kept
locally for the window, and only the demanded subset is forwarded to LeanSignal.
None of these windows is configurable through the agent; edit the store's own
plist or config to change one.

| Store | Window | Set in |
|---|---|---|
| VictoriaMetrics (metrics) | **1 day**, exact | `--retentionPeriod=1d` in `com.leansignal.victoria-metrics.plist` |
| Loki (logs) | **1 hour**, exact | `max_query_lookback: 1h` in `loki.yaml` (`retention_period: 2h` only bounds disk) |
| Tempo (traces) | **~1 hour**, approximate | `block_retention: 1h` in `tempo.yaml` — deletion is compaction-driven, so queries may see a little more |

> Note: macOS binaries from a release are not notarized; Gatekeeper may require
> approval the first time. Bundles installed via the script run as root daemons.

### Change the agent key or tenant

The live connection details are the agent LaunchDaemon's `EnvironmentVariables`
(launchd does **not** read `agent.env` on macOS). Edit the three `<string>` values in
**`/Library/LaunchDaemons/com.leansignal.agent.plist`**, then reload only the agent:

```bash
sudo nano /Library/LaunchDaemons/com.leansignal.agent.plist
```
```xml
<key>LEANSIGNAL_TENANT</key>    <string><tenant></string>
<key>LEANSIGNAL_AGENT_KEY</key> <string><key></string>
```
```bash
sudo launchctl unload /Library/LaunchDaemons/com.leansignal.agent.plist
sudo launchctl load -w /Library/LaunchDaemons/com.leansignal.agent.plist
```

Normally those are the only **two** values you touch: the agent resolves its
tenant's region from control-center at startup and derives every backend host
from the slug (gRPC control plus the metrics/logs/traces ingest hosts), so
changing tenant means changing the slug and the key — nothing else. The plist
also has empty `LEANSIGNAL_ENDPOINT` / `LEANSIGNAL_DATAPLANE_ENDPOINT` entries;
fill one in only to **pin** that host and bypass resolution for it (there are
matching `LEANSIGNAL_LOKI_ENDPOINT` / `LEANSIGNAL_TEMPO_ENDPOINT` overrides, and
`LEANSIGNAL_DOMAIN` pins the region and skips the lookup entirely). Or just
re-run the installer with `--agent-key` / `--tenant` (it rewrites these and keeps
your config + all local store data).

## Upgrading

Upgrade just the agent — the local stores and their data are untouched:
```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/upgrade.sh | sudo bash
```
See [Upgrading](upgrading.md) for agent-only vs VM upgrades, data safety, and
rollback. Loki and Tempo have no `--with-*` upgrade path — re-run the installer
to move them to a newer pinned version.

## Uninstall

Removes every binary and LaunchDaemon it installed. Keeps config + store data
unless you pass `--purge`.

**Download, then run** (clearest — `--purge` is a normal script argument):

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/uninstall.sh -o uninstall.sh
sudo bash uninstall.sh            # keep config + store data
sudo bash uninstall.sh --purge    # also delete config + store data
```

One-liner equivalent — `--purge` **must** come after `-s --` (that hands it to the
script; putting it on `curl` or `bash` errors with "unknown/invalid option"):

```bash
curl -fsSL https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/uninstall.sh | sudo bash -s -- --purge
```
