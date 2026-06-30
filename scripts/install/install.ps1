<#
.SYNOPSIS
  LeanSignal Agent installer for Windows. Installs the agent + local
  VictoriaMetrics and registers them as Windows services.

.EXAMPLE
  irm https://raw.githubusercontent.com/LeanSignal/leansignal-agent/main/scripts/install/install.ps1 | iex
  # or, with arguments (run from an elevated PowerShell):
  .\install.ps1 -AgentKey KEY -Endpoint wss://api.leansignal.com/api/v1/agents/ws/ -DataplaneEndpoint https://dataplane.example.com/api/v1/write
#>
[CmdletBinding()]
param(
  [string]$AgentKey,
  [string]$Endpoint,
  [string]$DataplaneEndpoint,
  [string]$Version = "latest",
  [string]$Repo = "LeanSignal/leansignal-agent",
  [switch]$NoVM
)

$ErrorActionPreference = "Stop"

function Info($m) { Write-Host "[leansignal] $m" -ForegroundColor Cyan }
function Die($m)  { Write-Host "[leansignal] ERROR: $m" -ForegroundColor Red; exit 1 }

# Require admin (needed to create services and write to Program Files).
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)) {
  Die "must run from an elevated (Administrator) PowerShell"
}

# Prompt for the required connection details when not supplied as parameters.
if (-not $Endpoint)          { $Endpoint = Read-Host "LeanSignal API WebSocket URL (e.g. wss://api.leansignal.com/api/v1/agents/ws/)" }
if (-not $DataplaneEndpoint) { $DataplaneEndpoint = Read-Host "Central dataplane remote-write URL (e.g. https://dataplane.example.com/api/v1/write)" }
if (-not $AgentKey) {
  $sec = Read-Host "Agent key / secret token" -AsSecureString
  $AgentKey = [Runtime.InteropServices.Marshal]::PtrToStringAuto(
    [Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec))
}

if (-not $Endpoint)          { Die "LeanSignal API URL is required (-Endpoint)" }
if (-not $AgentKey)          { Die "agent key is required (-AgentKey)" }
if (-not $DataplaneEndpoint) { Die "dataplane URL is required (-DataplaneEndpoint)" }

$arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { Die "unsupported architecture" }

# Resolve version
if ($Version -eq "latest") {
  Info "resolving latest release..."
  $rel = Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest"
  $Version = $rel.tag_name
}
$verNoV = $Version.TrimStart("v")
Info "installing version $Version ($arch)"

$installDir = Join-Path $env:ProgramFiles "LeanSignal\Agent"
$dataDir    = Join-Path $env:ProgramData "LeanSignal\Agent"
$confDir    = $dataDir
New-Item -ItemType Directory -Force -Path $installDir, $dataDir, (Join-Path $dataDir "vm") | Out-Null

# Download bundle
$bundle = "leansignal-agent-bundle_${verNoV}_windows_${arch}.zip"
$base   = "https://github.com/$Repo/releases/download/$Version"
$tmp    = Join-Path $env:TEMP "lsa-$verNoV"
New-Item -ItemType Directory -Force -Path $tmp | Out-Null
Info "downloading $bundle"
Invoke-WebRequest -Uri "$base/$bundle" -OutFile (Join-Path $tmp $bundle)

# Optional checksum verification
try {
  Invoke-WebRequest -Uri "$base/bundle-checksums.txt" -OutFile (Join-Path $tmp "bundle-checksums.txt")
  $want = (Select-String -Path (Join-Path $tmp "bundle-checksums.txt") -Pattern $bundle | Select-Object -First 1) -replace '\s.*$',''
  if ($want) {
    $got = (Get-FileHash (Join-Path $tmp $bundle) -Algorithm SHA256).Hash.ToLower()
    if ($want.ToLower() -ne $got) { Die "checksum mismatch for $bundle" }
    Info "checksum verified"
  }
} catch { Info "WARNING: could not verify checksum" }

Expand-Archive -Path (Join-Path $tmp $bundle) -DestinationPath $tmp -Force

Copy-Item (Join-Path $tmp "bin\leansignal-agent.exe") (Join-Path $installDir "leansignal-agent.exe") -Force
$installVM = -not $NoVM
if ($installVM) {
  $vmExe = Join-Path $tmp "bin\victoria-metrics.exe"
  if (Test-Path $vmExe) { Copy-Item $vmExe (Join-Path $installDir "victoria-metrics.exe") -Force }
  else { Info "WARNING: bundle has no VictoriaMetrics binary; skipping VM"; $installVM = $false }
}

# Config (don't clobber)
$cfgDst = Join-Path $confDir "config.yaml"
if (-not (Test-Path $cfgDst)) { Copy-Item (Join-Path $tmp "config\config.yaml") $cfgDst -Force }
else { Copy-Item (Join-Path $tmp "config\config.yaml") "$cfgDst.new" -Force; Info "existing config kept; template at $cfgDst.new" }

# Services (sc.exe). Environment is passed to the agent service via its registry Environment value.
if ($installVM) {
  $vmBin = '"{0}" --storageDataPath="{1}" --retentionPeriod=1d --httpListenAddr=127.0.0.1:8428' -f (Join-Path $installDir "victoria-metrics.exe"), (Join-Path $dataDir "vm")
  sc.exe create LeanSignalVictoriaMetrics binPath= $vmBin start= auto DisplayName= "LeanSignal VictoriaMetrics" | Out-Null
  sc.exe start LeanSignalVictoriaMetrics | Out-Null
}

$agentBin = '"{0}" --config "{1}"' -f (Join-Path $installDir "leansignal-agent.exe"), $cfgDst
sc.exe create LeanSignalAgent binPath= $agentBin start= auto DisplayName= "LeanSignal Agent" depend= LeanSignalVictoriaMetrics | Out-Null

# Per-service environment via registry (REG_MULTI_SZ Environment value)
$svcKey = "HKLM:\SYSTEM\CurrentControlSet\Services\LeanSignalAgent"
$envLines = @(
  "LEANSIGNAL_ENDPOINT=$Endpoint",
  "LEANSIGNAL_AGENT_KEY=$AgentKey",
  "LEANSIGNAL_DATAPLANE_ENDPOINT=$DataplaneEndpoint"
)
New-ItemProperty -Path $svcKey -Name Environment -PropertyType MultiString -Value $envLines -Force | Out-Null

sc.exe start LeanSignalAgent | Out-Null
Info "installed. Local VictoriaMetrics: http://127.0.0.1:8428 ; agent health: http://127.0.0.1:13133"
