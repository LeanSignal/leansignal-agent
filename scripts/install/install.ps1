<#
.SYNOPSIS
  LeanSignal Agent installer for Windows. Installs the agent + local
  VictoriaMetrics and registers them as Windows services.

.EXAMPLE
  # You need your agent key, an agent name, and the tenant; the gRPC + ingest
  # hosts are derived. Run from an elevated PowerShell:
  .\install.ps1 -AgentKey KEY -AgentName NAME -Tenant TENANT
  # Advanced: override with -Endpoint / -DataplaneEndpoint, or -Domain.
#>
[CmdletBinding()]
param(
  [string]$AgentKey,
  [string]$AgentName,
  [string]$Tenant,
  [string]$Domain = "eu11.leansignal.io",
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

# Prompt for what's needed when not supplied as parameters.
if ((-not $Endpoint) -and (-not $Tenant)) { $Tenant = Read-Host "Tenant name (control host becomes <tenant>-grpc.$Domain)" }
if (-not $AgentKey) {
  $sec = Read-Host "Agent key / secret token" -AsSecureString
  $AgentKey = [Runtime.InteropServices.Marshal]::PtrToStringAuto(
    [Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec))
}
if (-not $AgentName) { $AgentName = Read-Host "Agent name (identifies this host; becomes the agent_name label)" }

if (-not $AgentKey) { Die "agent key is required (-AgentKey)" }
if (-not $AgentName) { Die "agent name is required (-AgentName)" }
# The control + ingest hosts are derived from the tenant unless overridden.
if (((-not $Endpoint) -or (-not $DataplaneEndpoint)) -and (-not $Tenant)) {
  Die "tenant is required (-Tenant), or pass -Endpoint and -DataplaneEndpoint explicitly"
}
if (-not $Endpoint)          { $Endpoint = "${Tenant}-grpc.${Domain}:443" }
if (-not $DataplaneEndpoint) { $DataplaneEndpoint = "https://${Tenant}-ingest.${Domain}/api/v1/write" }
Info "control endpoint:  $Endpoint"
Info "dataplane endpoint: $DataplaneEndpoint"

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
  $mi = Select-String -Path (Join-Path $tmp "bundle-checksums.txt") -Pattern ([regex]::Escape($bundle)) | Select-Object -First 1
  $want = if ($mi) { (($mi.Line -split '\s+') | Where-Object { $_ })[0] }
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
  "LEANSIGNAL_AGENT_NAME=$AgentName",
  "LEANSIGNAL_DATAPLANE_ENDPOINT=$DataplaneEndpoint"
)
New-ItemProperty -Path $svcKey -Name Environment -PropertyType MultiString -Value $envLines -Force | Out-Null

sc.exe start LeanSignalAgent | Out-Null
Info "installed. Local VictoriaMetrics: http://127.0.0.1:8428 ; agent health: http://127.0.0.1:13133"
