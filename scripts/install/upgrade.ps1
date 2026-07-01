<#
.SYNOPSIS
  LeanSignal Agent upgrader for Windows.

  Upgrades an EXISTING install in place. By default only the agent (the
  OpenTelemetry Collector) is upgraded: it stops just the LeanSignalAgent
  service, swaps leansignal-agent.exe, and starts it again. VictoriaMetrics and
  its on-disk data (%ProgramData%\LeanSignal\Agent\vm) are never touched.

  With -WithVM it ALSO upgrades VictoriaMetrics: it snapshots first (and ABORTS
  if the snapshot can't be confirmed), then swaps the VM binary against the SAME
  --storageDataPath, so existing data is kept. The snapshot is removed once the
  upgrade is healthy.

  The binary is rolled back automatically if the service does not come back healthy.

.EXAMPLE
  # agent -> latest, VM untouched (run from an elevated PowerShell)
  .\upgrade.ps1
.EXAMPLE
  .\upgrade.ps1 -Version v0.2.0
.EXAMPLE
  .\upgrade.ps1 -WithVM          # also upgrade VictoriaMetrics (snapshots first)
#>
[CmdletBinding()]
param(
  [string]$Version = "latest",
  [string]$Repo = "LeanSignal/leansignal-agent",
  [switch]$WithVM,
  [string]$VmVersion,
  [switch]$SkipSnapshot
)

$ErrorActionPreference = "Stop"

function Info($m) { Write-Host "[leansignal] $m" -ForegroundColor Cyan }
function Warn($m) { Write-Host "[leansignal] WARNING: $m" -ForegroundColor Yellow }
function Die($m)  { Write-Host "[leansignal] ERROR: $m" -ForegroundColor Red; exit 1 }

# Start a service without letting a failure become terminating — rollback paths
# must always attempt every service start (the agent depends on the VM service).
function Start-Svc($name) { try { Start-Service $name } catch { Warn "failed to start ${name}: $_" } }

$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltinRole]::Administrator)) {
  Die "must run from an elevated (Administrator) PowerShell"
}

$arch = if ([Environment]::Is64BitOperatingSystem) { "amd64" } else { Die "unsupported architecture" }
$installDir = Join-Path $env:ProgramFiles "LeanSignal\Agent"
$dataDir    = Join-Path $env:ProgramData "LeanSignal\Agent"
$vmData     = Join-Path $dataDir "vm"
$agentExe   = Join-Path $installDir "leansignal-agent.exe"
$vmExe      = Join-Path $installDir "victoria-metrics.exe"

if (-not (Test-Path $agentExe)) { Die "no existing agent at $agentExe — use install.ps1 for a fresh install" }

function Wait-Healthy($url, $label) {
  for ($i = 0; $i -lt 30; $i++) {
    try { Invoke-WebRequest -UseBasicParsing -Uri $url -TimeoutSec 3 | Out-Null; Info "$label healthy"; return $true }
    catch { Start-Sleep -Seconds 1 }
  }
  return $false
}

# first "X.Y.Z" (optionally -prerelease) token in a --version banner, or ""
function Get-Semver($s) { [regex]::Match([string]$s, '\d+\.\d+\.\d+(-[0-9A-Za-z.]+)?').Value }

# Resolve target agent version
if ($Version -eq "latest") {
  Info "resolving latest release..."
  $Version = (Invoke-RestMethod "https://api.github.com/repos/$Repo/releases/latest").tag_name
}
$verNoV = $Version.TrimStart("v")
$base   = "https://github.com/$Repo/releases/download/$Version"
$tmp    = Join-Path $env:TEMP ("lsa-upg-" + $verNoV)
New-Item -ItemType Directory -Force -Path $tmp | Out-Null

$curVer = (& $agentExe --version 2>$null | Select-Object -First 1)
Info "current agent: $curVer"
Info "target release: $Version"

# Exact-version skip (parsed semver equality, so 0.2.10 != 0.2.1)
$curSem = Get-Semver $curVer
if ((-not $WithVM) -and $curSem -and ($curSem -eq $verNoV)) {
  Info "agent already at $Version; nothing to do"; exit 0
}

function Get-ReleaseFile($name, [switch]$Optional) {
  try { Invoke-WebRequest -UseBasicParsing -Uri "$base/$name" -OutFile (Join-Path $tmp $name); return $true }
  catch { if ($Optional) { return $false } else { Die "download failed: $base/$name" } }
}
# Fail-CLOSED integrity check: read the matched line's .Line (NOT the stringified
# MatchInfo, which is "<path>:<lineno>:<content>"), and abort if the file or the
# asset's line is missing — a real release always ships the checksum file.
function Confirm-Checksum($asset, $sumsFile) {
  $p = Join-Path $tmp $sumsFile
  if (-not (Test-Path $p)) { Die "checksum file $sumsFile unavailable; refusing to install $asset unverified" }
  $mi = Select-String -Path $p -Pattern ([regex]::Escape($asset)) | Select-Object -First 1
  if (-not $mi) { Die "no pinned checksum for $asset in $sumsFile; refusing to install unverified" }
  $want = (($mi.Line -split '\s+') | Where-Object { $_ })[0]
  $got  = (Get-FileHash (Join-Path $tmp $asset) -Algorithm SHA256).Hash.ToLower()
  if ($want.ToLower() -ne $got) { Die "checksum mismatch for $asset (aborting, nothing changed)" }
  Info "checksum verified: $asset"
}

# ============================ AGENT UPGRADE ==================================
# Uses the agent-only archive (just the collector .exe), not the full bundle.
$agentZip = "leansignal-agent_${verNoV}_windows_${arch}.zip"
Info "downloading $agentZip"
Get-ReleaseFile $agentZip | Out-Null
Get-ReleaseFile "checksums.txt" | Out-Null
Confirm-Checksum $agentZip "checksums.txt"

$ax = Join-Path $tmp "agent"; New-Item -ItemType Directory -Force -Path $ax | Out-Null
Expand-Archive -Path (Join-Path $tmp $agentZip) -DestinationPath $ax -Force
$newBin = (Get-ChildItem -Path $ax -Recurse -Filter "leansignal-agent.exe" | Select-Object -First 1).FullName
if (-not $newBin) { Die "leansignal-agent.exe not found inside $agentZip" }

$backup = "$agentExe.prev"
Info "backing up current agent -> $backup"
Copy-Item $agentExe $backup -Force

Info "stopping the agent service (VictoriaMetrics keeps running)"
Stop-Service LeanSignalAgent -Force
Copy-Item $newBin $agentExe -Force
Start-Svc LeanSignalAgent

if (Wait-Healthy "http://127.0.0.1:13133/" "agent") {
  $newVer = (& $agentExe --version 2>$null | Select-Object -First 1)
  Info "agent upgraded: $curVer -> $newVer"
  Remove-Item $backup -Force -ErrorAction SilentlyContinue
} else {
  Warn "agent did not become healthy — rolling back to the previous binary"
  Stop-Service LeanSignalAgent -Force
  Copy-Item $backup $agentExe -Force
  Start-Svc LeanSignalAgent
  Die "agent upgrade failed and was rolled back (VictoriaMetrics data untouched)"
}

# ============================ VM UPGRADE (opt) ===============================
if ($WithVM) {
  $vmSvc = Get-Service LeanSignalVictoriaMetrics -ErrorAction SilentlyContinue
  if ((-not $vmSvc) -or (-not (Test-Path $vmExe))) {
    Warn "no local VictoriaMetrics installed here; skipping -WithVM"
  } else {
    $vmver = $VmVersion
    if (-not $vmver -and (Get-ReleaseFile "VERSIONS.txt" -Optional)) {
      $mi = Select-String -Path (Join-Path $tmp "VERSIONS.txt") -Pattern '^victoria-metrics=' | Select-Object -First 1
      if ($mi) { $vmver = ($mi.Line -replace '^victoria-metrics=','').Trim() }
    }
    if (-not $vmver) { Die "could not determine target VictoriaMetrics version; pass -VmVersion X.Y.Z" }

    $curVm = (& $vmExe --version 2>$null | Select-Object -First 1)
    $curVmSem = Get-Semver $curVm
    Info "current VM: $curVm ; target VM: v$vmver"
    if ($curVmSem -and ($curVmSem -eq $vmver)) {
      Info "VictoriaMetrics already at v$vmver; skipping VM upgrade"
    } else {
      # Instant snapshot before touching VM — the only data-level rollback point,
      # so an unconfirmed snapshot ABORTS before any change.
      $snapName = $null
      if (-not $SkipSnapshot) {
        Info "creating a VictoriaMetrics snapshot before upgrading"
        try { $snap = Invoke-RestMethod -Uri "http://127.0.0.1:8428/snapshot/create" -TimeoutSec 15 }
        catch { Die "snapshot request failed; aborting before touching VM (use -SkipSnapshot to override)" }
        if ($snap.status -ne 'ok') { Die "snapshot not confirmed (status: $($snap.status)); aborting before touching VM" }
        $snapName = $snap.snapshot
        Info "snapshot ok: $snapName"
      } else {
        Warn "-SkipSnapshot: proceeding without a pre-upgrade snapshot"
      }

      $vmZip = "victoria-metrics-windows-${arch}-v${vmver}.zip"
      Info "downloading $vmZip"
      Get-ReleaseFile $vmZip | Out-Null
      Get-ReleaseFile "bundle-checksums.txt" | Out-Null
      Confirm-Checksum $vmZip "bundle-checksums.txt"

      $vx = Join-Path $tmp "vm"; New-Item -ItemType Directory -Force -Path $vx | Out-Null
      Expand-Archive -Path (Join-Path $tmp $vmZip) -DestinationPath $vx -Force
      $newVm = (Get-ChildItem -Path $vx -Recurse -Filter "victoria-metrics*.exe" | Select-Object -First 1).FullName
      if (-not $newVm) { Die "victoria-metrics.exe not found inside $vmZip" }

      $vmBackup = "$vmExe.prev"
      Copy-Item $vmExe $vmBackup -Force
      # Agent depends on the VM service, so stop the agent first, then VM.
      Info "stopping services to swap VictoriaMetrics (data at $vmData is preserved)"
      Stop-Service LeanSignalAgent -Force
      Stop-Service LeanSignalVictoriaMetrics -Force
      Copy-Item $newVm $vmExe -Force
      Start-Svc LeanSignalVictoriaMetrics
      Start-Svc LeanSignalAgent

      if (Wait-Healthy "http://127.0.0.1:8428/health" "VictoriaMetrics") {
        Info "VictoriaMetrics upgraded -> v$vmver"
        Remove-Item $vmBackup -Force -ErrorAction SilentlyContinue
        if ($snapName) { try { Invoke-RestMethod -Uri "http://127.0.0.1:8428/snapshot/delete?snapshot=$snapName" -TimeoutSec 15 | Out-Null; Info "removed pre-upgrade snapshot $snapName" } catch { } }
      } else {
        Warn "VictoriaMetrics did not become healthy — rolling back"
        Stop-Service LeanSignalAgent -Force
        Stop-Service LeanSignalVictoriaMetrics -Force
        Copy-Item $vmBackup $vmExe -Force
        Start-Svc LeanSignalVictoriaMetrics
        Start-Svc LeanSignalAgent
        if ($snapName) { Warn "kept snapshot $snapName under $vmData\snapshots for manual recovery" }
        Die "VictoriaMetrics upgrade failed and was rolled back (data dir untouched)"
      }
    }
  }
}

Remove-Item $tmp -Recurse -Force -ErrorAction SilentlyContinue
Info "upgrade complete. health: http://127.0.0.1:13133  vm: http://127.0.0.1:8428"
