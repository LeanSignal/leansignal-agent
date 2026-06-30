# Install on Windows

Installs the agent and a co-located VictoriaMetrics as **Windows services**.
Run from an **elevated** (Administrator) PowerShell. amd64 is supported.

## Install

```powershell
# Download and run with arguments:
$u = "https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/install.ps1"
Invoke-WebRequest $u -OutFile install.ps1
.\install.ps1 -AgentKey YOUR_KEY `
  -Endpoint api.leansignal.com:443 `
  -DataplaneEndpoint https://dataplane.example.com/api/v1/write
```

### Parameters

| Parameter | Meaning |
|-----------|---------|
| `-AgentKey` | agent auth key (required) |
| `-Endpoint` | LeanSignal gRPC endpoint (host:port) (required) |
| `-DataplaneEndpoint` | central remote-write URL (required) |
| `-Version` | specific version (default: latest) |
| `-NoVM` | don't install the local VictoriaMetrics |

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

```powershell
Get-Service LeanSignalAgent, LeanSignalVictoriaMetrics
Restart-Service LeanSignalAgent
```

## Uninstall

```powershell
.\uninstall.ps1            # keep config/data
.\uninstall.ps1 -Purge     # also remove config/data
```
