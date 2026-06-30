<#
.SYNOPSIS
  Uninstall the LeanSignal Agent + local VictoriaMetrics on Windows.
.PARAMETER Purge
  Also delete config and data under %ProgramData%\LeanSignal\Agent.
#>
param([switch]$Purge)
$ErrorActionPreference = "SilentlyContinue"

function Info($m) { Write-Host "[leansignal] $m" -ForegroundColor Cyan }

foreach ($svc in @("LeanSignalAgent", "LeanSignalVictoriaMetrics")) {
  sc.exe stop $svc | Out-Null
  sc.exe delete $svc | Out-Null
}

$installDir = Join-Path $env:ProgramFiles "LeanSignal\Agent"
Remove-Item -Recurse -Force $installDir
Info "removed services and binaries"

if ($Purge) {
  Remove-Item -Recurse -Force (Join-Path $env:ProgramData "LeanSignal\Agent")
  Info "purged config and data"
} else {
  Info "kept config/data under %ProgramData%\LeanSignal\Agent (use -Purge to remove)"
}
