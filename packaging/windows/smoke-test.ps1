# smoke-test.ps1 — run the Windows artifacts on a Windows machine (CI: windows-latest).
#
#   ./packaging/windows/smoke-test.ps1 -Dist dist
#
# Portable exe: `version`, then `serve` on a fixed port until api/health answers,
# then `stop`. Setup: silent install, the installed exe runs, silent uninstall.
param([string]$Dist = "dist", [int]$Port = 18734)
$ErrorActionPreference = "Stop"

$portable = Get-ChildItem "$Dist/agent-keepalive-*-portable-x64.exe" | Select-Object -First 1
$setup = Get-ChildItem "$Dist/agent-keepalive-*-setup-x64.exe" | Select-Object -First 1
if (-not $portable -or -not $setup) { throw "no x64 portable/setup exe in $Dist" }

# The exes are GUI-subsystem programs (-H windowsgui): PowerShell would not wait
# for `& exe`, so run them with Start-Process and redirected output.
function Invoke-Exe([string]$Exe, [string[]]$Arguments) {
  $out = New-TemporaryFile
  $p = Start-Process -FilePath $Exe -ArgumentList $Arguments -Wait -PassThru -NoNewWindow `
    -RedirectStandardOutput $out.FullName -RedirectStandardError "$($out.FullName).err"
  Get-Content $out.FullName, "$($out.FullName).err" | Write-Host
  return $p.ExitCode
}

Write-Host "== $($portable.Name) version"
$rc = Invoke-Exe $portable.FullName @("version")
if ($rc -ne 0) { throw "version exited $rc" }

Write-Host "== serve on port $Port"
$data = Join-Path $env:RUNNER_TEMP "ak-smoke-data"
$server = Start-Process -FilePath $portable.FullName -ArgumentList @("serve", "--port", "$Port", "--data-dir", $data) -PassThru -NoNewWindow `
  -RedirectStandardOutput (Join-Path $env:RUNNER_TEMP "ak-serve.out") -RedirectStandardError (Join-Path $env:RUNNER_TEMP "ak-serve.err")
$ok = $false
for ($i = 0; $i -lt 60 -and -not $ok; $i++) {
  Start-Sleep -Milliseconds 500
  try {
    $r = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:$Port/api/health" -TimeoutSec 2
    if ($r.StatusCode -eq 200) { $ok = $true; Write-Host "health: $($r.Content)" }
  } catch { }
  if ($server.HasExited) { throw "server exited with $($server.ExitCode)" }
}
$null = Invoke-Exe $portable.FullName @("stop", "--data-dir", $data)
if (-not $server.WaitForExit(10000)) { Stop-Process -Id $server.Id -Force }
if (-not $ok) { throw "api/health never answered" }

Write-Host "== $($setup.Name) /S"
$p = Start-Process -FilePath $setup.FullName -ArgumentList "/S" -Wait -PassThru
if ($p.ExitCode -ne 0) { throw "setup exited $($p.ExitCode)" }
$installed = Join-Path $env:LOCALAPPDATA "Programs\agent-keepalive\agent-keepalive.exe"
if (-not (Test-Path $installed)) { throw "not installed at $installed" }
$rc = Invoke-Exe $installed @("version")
if ($rc -ne 0) { throw "installed version exited $rc" }
if (-not (Test-Path (Join-Path $env:APPDATA "Microsoft\Windows\Start Menu\Programs\agent-keepalive.lnk"))) {
  throw "no Start-menu shortcut"
}

Write-Host "== uninstall /S"
$un = Join-Path $env:LOCALAPPDATA "Programs\agent-keepalive\uninstall.exe"
# The uninstaller copies itself to %TEMP% and returns at once; _?= runs it in place.
$p = Start-Process -FilePath $un -ArgumentList @("/S", "_?=$(Split-Path $un)") -Wait -PassThru
if ($p.ExitCode -ne 0) { throw "uninstall exited $($p.ExitCode)" }
if (Test-Path $installed) { throw "still installed after uninstall" }
Write-Host "smoke test passed"
