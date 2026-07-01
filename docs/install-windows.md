# Install on Windows

Installs the agent and a co-located VictoriaMetrics as **Windows services**.
Run from an **elevated** (Administrator) PowerShell. amd64 is supported.

## Install

```powershell
# Download and run (you only need your agent key + tenant):
$u = "https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/install.ps1"
Invoke-WebRequest $u -OutFile install.ps1
.\install.ps1 -AgentKey YOUR_KEY -Tenant YOUR_TENANT
```

### Parameters

| Parameter | Meaning |
|-----------|---------|
| `-AgentKey` | agent auth key (required) |
| `-Tenant` | tenant name; derives `<tenant>-grpc.<domain>:443` and `…-ingest.<domain>` (required unless `-Endpoint` is given) |
| `-Domain` | cluster domain (default: `eu11.leansignal.io`) |
| `-Endpoint` | advanced: gRPC control host `host:port`, overrides the derived one |
| `-DataplaneEndpoint` | advanced: remote-write URL, overrides the derived one |
| `-Version` | specific version (default: latest) |
| `-NoVM` | don't install the local VictoriaMetrics |

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
