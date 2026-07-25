# Install on Windows

Installs the agent and a co-located VictoriaMetrics (local metrics store) as
**Windows services**. Run from an **elevated** (Administrator) PowerShell. amd64
is supported.

> The agent collects **telemetry** — metrics, logs, and traces — and its OTLP
> endpoints accept all three. On Windows, though, only the co-located **metrics**
> store (VictoriaMetrics) is installed. Co-located **log and trace** stores (Loki,
> Tempo) are installed automatically on [Linux](install-linux.md) and
> [macOS](install-macos.md), but have no Windows equivalent yet. On Windows, logs
> and traces are still received, and demanded ones are still forwarded to
> LeanSignal — what you lose is the **local** full-fidelity buffer and the ability
> to explore logs/traces before demanding them. See
> [Logs and traces on Windows](#logs-and-traces-on-windows) below.

## Install

```powershell
# Download and run (you need your agent key, an agent name, and the tenant):
$u = "https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/install.ps1"
Invoke-WebRequest $u -OutFile install.ps1
.\install.ps1 -AgentKey YOUR_KEY -AgentName this-host -Tenant YOUR_TENANT
```

### Parameters

| Parameter | Meaning |
|-----------|---------|
| `-AgentKey` | agent auth key (required, both modes) |
| `-AgentName` | name identifying this agent/host; becomes the `leansignal_agent_name` label on every metric (required, both modes) |
| `-CentralUrl` | install in **edge** mode: forward OTLP to this central agent (`host:port`, plaintext). Also via `CENTRAL_AGENT_GRPC_URL`. No local VM; `-Tenant` not needed |
| `-Tenant` | tenant slug (required for **central** mode). The agent resolves the tenant's region from control-center at startup and derives every backend host from it — gRPC control plus the metrics/logs/traces ingest hosts |
| `-Version` | specific version (default: latest) |
| `-NoVM` | don't install the local VictoriaMetrics |

Advanced — each of these **pins** one value and skips resolution for it; leave
them unset to let the agent derive everything from `-Tenant`:

| Parameter | Meaning |
|-----------|---------|
| `-Domain` | region domain (e.g. `eu11.leansignal.io`); skips the control-center lookup entirely |
| `-Endpoint` | gRPC control host `host:port` |
| `-DataplaneEndpoint` | metrics ingest base URL |
| `-LokiEndpoint` | logs ingest base URL |
| `-TempoEndpoint` | traces ingest base URL |
| `-CcUrl` | control-center origin (default `https://cc.leansignal.io`) |
| `-ResolveAat` | resolve token (has a public default) |

> There is no `-NoLoki` / `-NoTempo` on Windows — those stores are not installed
> on Windows at all, so there is nothing to opt out of.

## It's already collecting

The installer creates and starts the Windows services, so the agent is running
now. **Host metrics — CPU, memory, disk, network — are collected automatically**;
nothing else to configure. Verify:

```powershell
Invoke-RestMethod http://127.0.0.1:13133/                              # health check
Invoke-RestMethod http://127.0.0.1:8428/api/v1/label/__name__/values   # metric names in the local store
```

To send your own application metrics, point any OpenTelemetry SDK at the agent's
OTLP endpoint (`http://127.0.0.1:4318` for HTTP, `:4317` for gRPC).

## Logs and traces on Windows

Local log/trace stores are installed automatically on **Linux and macOS**; there
is no Windows equivalent today, and no `-NoLoki`/`-NoTempo` flag because nothing
is installed to opt out of. The installer does lay down the **same collector
config as Linux**, which still contains the full logs and traces pipelines. In
practice:

| | on Windows |
|---|---|
| OTLP logs/traces accepted on `:4317` / `:4318` | yes |
| Loki push accepted on `:3500` / `:3600` | yes |
| Demanded logs/traces forwarded to your tenant | yes |
| Kept locally at full fidelity | **no** — no local store installed |
| Explore/"Available" before you demand | **no** — that reads the local store |

The practical consequence is discovery: on Linux you can browse everything the
agent sees and *then* choose what to demand. On Windows there is nothing local to
browse, so demand selectors have to be written directly.

Because the shipped config still points its local exporters at
`127.0.0.1:3100` (Loki) and `127.0.0.1:4328` (Tempo), the agent log shows
repeated connection-refused export errors, and the `leansignal-aloki` /
`leansignal-atempo` scrape jobs report `up=0`. Both are harmless — the tenant
path is a separate pipeline and is unaffected. To quiet them, edit
`%ProgramData%\LeanSignal\Agent\config.yaml`, remove `otlphttp/loki_local` and
`otlphttp/tempo_local` from their pipelines' `exporters:` lists and drop the two
scrape jobs from `prometheus/localstores`, then restart the service. If this host
sends no logs or traces at all, you can delete the `logs/*` and `traces/*`
pipelines outright.

## What it installs

| Path | |
|------|---|
| `%ProgramFiles%\LeanSignal\Agent\leansignal-agent.exe`, `victoria-metrics.exe` | binaries |
| `%ProgramData%\LeanSignal\Agent\config.yaml` | collector config |
| `%ProgramData%\LeanSignal\Agent\vm` | local VM data |
| Services `LeanSignalAgent`, `LeanSignalVictoriaMetrics` | Windows services |

The agent's environment (endpoint, key, dataplane) is stored on the service's
registry `Environment` value.

## Manage

Two **independent** Windows services — the collector (`LeanSignalAgent`) and the
local store (`LeanSignalVictoriaMetrics`). The agent depends on the VM service, so
stopping VM also stops the agent; the agent can be restarted on its own.

```powershell
# status of both
Get-Service LeanSignalAgent, LeanSignalVictoriaMetrics

# AGENT — start / stop / restart (VictoriaMetrics keeps running)
Restart-Service LeanSignalAgent
Stop-Service    LeanSignalAgent
Start-Service   LeanSignalAgent

# VICTORIA-METRICS — restart (also cycles the dependent agent)
Restart-Service LeanSignalVictoriaMetrics -Force
```

Local store: `http://127.0.0.1:8428` · agent health: `http://127.0.0.1:13133`.

### Local VM retention

The local store keeps a **fixed 1 day (24h)** of data by design — it's a short edge
buffer (full fidelity is kept locally; only the demanded subset is forwarded to the
central dataplane). It's set to `--retentionPeriod=1d` on the
`LeanSignalVictoriaMetrics` service and is not a configurable option.

### Change the agent key or tenant

The agent's connection details are stored on the `LeanSignalAgent` service's registry
`Environment` value. Simplest is to re-run the installer:

```powershell
.\install.ps1 -AgentKey NEW_KEY -Tenant NEW_TENANT
```
(keeps your config + VM data). Advanced — set the registry value directly, then restart:
```powershell
$k = 'HKLM:\SYSTEM\CurrentControlSet\Services\LeanSignalAgent'
Set-ItemProperty -Path $k -Name Environment -Value @(
  "LEANSIGNAL_TENANT=NEW_TENANT",
  "LEANSIGNAL_AGENT_KEY=NEW_KEY",
  "LEANSIGNAL_AGENT_NAME=this-host"
)
Restart-Service LeanSignalAgent
```
Changing the **tenant** normally means changing just the slug and the key: the
agent resolves the region from control-center at startup and derives every
backend host from `LEANSIGNAL_TENANT`. Add a `LEANSIGNAL_{ENDPOINT,
DATAPLANE_ENDPOINT,LOKI_ENDPOINT,TEMPO_ENDPOINT}` entry only to **pin** that host
and bypass resolution for it, or `LEANSIGNAL_DOMAIN` to pin the region and skip
the lookup entirely.

## Upgrading

Upgrade just the agent — VictoriaMetrics and its data are untouched. From an elevated PowerShell:
```powershell
iwr https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/upgrade.ps1 -OutFile upgrade.ps1; .\upgrade.ps1
```
See [Upgrading](upgrading.md) for agent-only vs VM upgrades, data safety, and rollback.

## Uninstall

```powershell
.\uninstall.ps1            # keep config/data
.\uninstall.ps1 -Purge     # also remove config/data
```
