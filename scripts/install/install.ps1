<#
.SYNOPSIS
  LeanSignal Agent installer for Windows. Installs the agent + local
  VictoriaMetrics and registers them as Windows services.

.EXAMPLE
  # You need your agent key, an agent name, and the tenant slug. The agent
  # resolves its region from control-center at startup and derives the gRPC +
  # ingest hosts. Run from an elevated PowerShell:
  .\install.ps1 -AgentKey KEY -AgentName NAME -Tenant SLUG
  # Advanced: -Domain pins the region (skips the lookup); -Endpoint /
  # -DataplaneEndpoint / -LokiEndpoint / -TempoEndpoint pin individual hosts.
#>
[CmdletBinding()]
param(
  [string]$AgentKey,
  [string]$AgentName,
  # EDGE mode when set (or via the CENTRAL_AGENT_GRPC_URL env var): forward OTLP to
  # this central agent, install no local VM, and require no tenant.
  [string]$CentralUrl = $env:CENTRAL_AGENT_GRPC_URL,
  [string]$Tenant,
  # Region domain. Empty → resolve from control-center at startup. Set to pin the
  # region and skip that lookup.
  [string]$Domain = "",
  [string]$CcUrl,
  [string]$ResolveAat,
  [string]$Endpoint,
  [string]$DataplaneEndpoint,
  [string]$LokiEndpoint,
  [string]$TempoEndpoint,
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

$mode = if ($CentralUrl) { "edge" } else { "central" }
if ($mode -eq "edge") { $NoVM = $true }
Info "install mode: $mode"

# Prompt for what's needed when not supplied as parameters.
if (($mode -eq "central") -and (-not $Endpoint) -and (-not $Tenant)) { $Tenant = Read-Host "Tenant slug (the agent resolves its region + hosts from control-center)" }
if (-not $AgentKey) {
  $sec = Read-Host "Agent key / secret token" -AsSecureString
  $AgentKey = [Runtime.InteropServices.Marshal]::PtrToStringAuto(
    [Runtime.InteropServices.Marshal]::SecureStringToBSTR($sec))
}
if (-not $AgentName) { $AgentName = Read-Host "Agent name (identifies this host; becomes the agent_name label)" }

if (-not $AgentKey) { Die "agent key is required (-AgentKey)" }
if (-not $AgentName) { Die "agent name is required (-AgentName)" }
if ($mode -eq "central") {
  # The agent resolves the tenant's region from control-center at startup and
  # derives every backend host from the slug (${leansignal:...} provider). Only
  # the slug is needed unless the gRPC + metrics hosts are both pinned.
  if (((-not $Endpoint) -or (-not $DataplaneEndpoint)) -and (-not $Tenant)) {
    Die "tenant is required (-Tenant), or pin the hosts explicitly (-Endpoint and -DataplaneEndpoint)"
  }
  if ($Tenant) {
    if ($Domain) { Info "tenant: $Tenant  (region pinned: $Domain — control-center lookup skipped)" }
    else         { Info "tenant: $Tenant  (region resolved from control-center at startup)" }
  }
  if ($Endpoint)          { Info "gRPC host pinned:    $Endpoint" }
  if ($DataplaneEndpoint) { Info "metrics host pinned: $DataplaneEndpoint" }
} else {
  Info "central agent (OTLP): $CentralUrl"
}

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

# Config (don't clobber) - edge ships a separate template in the bundle
$cfgSrc = if ($mode -eq "edge") { Join-Path $tmp "config\config-edge.yaml" } else { Join-Path $tmp "config\config.yaml" }
if (-not (Test-Path $cfgSrc)) { Die "bundle is missing $(Split-Path $cfgSrc -Leaf) (need a newer release for edge mode)" }
$cfgDst = Join-Path $confDir "config.yaml"
if (-not (Test-Path $cfgDst)) { Copy-Item $cfgSrc $cfgDst -Force }
else { Copy-Item $cfgSrc "$cfgDst.new" -Force; Info "existing config kept; template at $cfgDst.new" }

# Services (sc.exe). Environment is passed to the agent service via its registry Environment value.
if ($installVM) {
  $vmBin = '"{0}" --storageDataPath="{1}" --retentionPeriod=1d --httpListenAddr=127.0.0.1:8428' -f (Join-Path $installDir "victoria-metrics.exe"), (Join-Path $dataDir "vm")
  sc.exe create LeanSignalVictoriaMetrics binPath= $vmBin start= auto DisplayName= "LeanSignal VictoriaMetrics" | Out-Null
  sc.exe start LeanSignalVictoriaMetrics | Out-Null
}

$agentBin = '"{0}" --config "{1}"' -f (Join-Path $installDir "leansignal-agent.exe"), $cfgDst
if ($installVM) {
  sc.exe create LeanSignalAgent binPath= $agentBin start= auto DisplayName= "LeanSignal Agent" depend= LeanSignalVictoriaMetrics | Out-Null
} else {
  sc.exe create LeanSignalAgent binPath= $agentBin start= auto DisplayName= "LeanSignal Agent" | Out-Null
}

# Per-service environment via registry (REG_MULTI_SZ Environment value)
$svcKey = "HKLM:\SYSTEM\CurrentControlSet\Services\LeanSignalAgent"
$envLines = if ($mode -eq "edge") {
  @(
    "LEANSIGNAL_AGENT_KEY=$AgentKey",
    "LEANSIGNAL_AGENT_NAME=$AgentName",
    "CENTRAL_AGENT_GRPC_URL=$CentralUrl"
  )
} else {
  # The slug drives the ${leansignal:...} startup resolve; hosts are written only
  # when explicitly pinned (each becomes a verbatim override).
  $lines = [System.Collections.Generic.List[string]]::new()
  $lines.Add("LEANSIGNAL_TENANT=$Tenant")
  $lines.Add("LEANSIGNAL_AGENT_KEY=$AgentKey")
  $lines.Add("LEANSIGNAL_AGENT_NAME=$AgentName")
  if ($CcUrl)            { $lines.Add("LEANSIGNAL_CC_URL=$CcUrl") }
  if ($ResolveAat)       { $lines.Add("LEANSIGNAL_RESOLVE_AAT=$ResolveAat") }
  if ($Domain)           { $lines.Add("LEANSIGNAL_DOMAIN=$Domain") }
  if ($Endpoint)         { $lines.Add("LEANSIGNAL_ENDPOINT=$Endpoint") }
  if ($DataplaneEndpoint){ $lines.Add("LEANSIGNAL_DATAPLANE_ENDPOINT=$DataplaneEndpoint") }
  if ($LokiEndpoint)     { $lines.Add("LEANSIGNAL_LOKI_ENDPOINT=$LokiEndpoint") }
  if ($TempoEndpoint)    { $lines.Add("LEANSIGNAL_TEMPO_ENDPOINT=$TempoEndpoint") }
  $lines.ToArray()
}
New-ItemProperty -Path $svcKey -Name Environment -PropertyType MultiString -Value $envLines -Force | Out-Null

sc.exe start LeanSignalAgent | Out-Null
Info "installed. Local VictoriaMetrics: http://127.0.0.1:8428 ; agent health: http://127.0.0.1:13133"
