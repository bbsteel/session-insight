# Windows-native counterpart to ./run.sh for start/stop/status/all.
# Usage:
#   .\run.ps1 start
#   .\run.ps1 stop
#   .\run.ps1 status
#   .\run.ps1 all     # build frontend + go, then start
param(
  [Parameter(Position = 0)]
  [ValidateSet("start", "stop", "status", "all", "build", "restart")]
  [string]$Command = "start"
)

$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location $Root

$Bin = Join-Path $Root "session-insight.exe"
$Log = Join-Path $Root "session-insight.log"
$ErrLog = Join-Path $Root "session-insight.log.stderr"
$PidFile = Join-Path $Root "session-insight.pid"
$UrlFile = Join-Path $Root "session-insight.url"
$Port = if ($env:PORT) { $env:PORT } else { "8080" }
$DataDir = if ($env:SI_DATA_DIR) { $env:SI_DATA_DIR } else { "" }
$StartScript = Join-Path $Root "scripts\windows-start.ps1"

function Get-OwnedProcess {
  if (-not (Test-Path $PidFile)) { return $null }
  $procId = (Get-Content $PidFile -Raw).Trim()
  if ($procId -notmatch '^\d+$') { return $null }
  $p = Get-Process -Id ([int]$procId) -ErrorAction SilentlyContinue
  if (-not $p) { return $null }
  if ($p.Path -and ($p.Path -ieq $Bin)) { return $p }
  return $null
}

function Stop-App {
  $p = Get-OwnedProcess
  if ($p) {
    Write-Host "==> Stopping SessionInsight (PID: $($p.Id))"
    Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue
    Start-Sleep -Milliseconds 400
  } else {
    # Fall back: stop any instance of this exact binary path.
    Get-CimInstance Win32_Process -Filter "name = 'session-insight.exe'" -ErrorAction SilentlyContinue |
      Where-Object { $_.ExecutablePath -and ($_.ExecutablePath -ieq $Bin) } |
      ForEach-Object {
        Write-Host "==> Stopping SessionInsight (PID: $($_.ProcessId))"
        Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue
      }
  }
  Remove-Item $PidFile, $UrlFile -ErrorAction SilentlyContinue
}

function Start-App {
  if (-not (Test-Path $Bin)) {
    throw "binary not found at $Bin — run: .\run.ps1 build"
  }
  Stop-App
  Write-Host "==> Starting SessionInsight (background)"
  Write-Host "    URL: http://127.0.0.1:$Port/"
  Write-Host "    Binary: $Bin"
  Write-Host "    Log: $Log"

  $args = @{
    BinPath    = $Bin
    WorkDir    = $Root
    LogPath    = $Log
    ErrLogPath = $ErrLog
    Port       = $Port
  }
  if ($DataDir) { $args.DataDir = $DataDir }

  $procId = & $StartScript @args
  if (-not $procId -or "$procId" -notmatch '^\d+$') {
    throw "failed to start (no PID)"
  }
  "$procId" | Set-Content $PidFile -Encoding ascii
  Write-Host "    PID: $procId"

  $url = $null
  for ($i = 0; $i -lt 150; $i++) {
    foreach ($f in @($Log, $ErrLog)) {
      if (Test-Path $f) {
        $m = Select-String -Path $f -Pattern 'SessionInsight listening on (http\S+)' -ErrorAction SilentlyContinue |
          Select-Object -Last 1
        if ($m) { $url = $m.Matches[0].Groups[1].Value; break }
      }
    }
    if (-not $url) {
      try {
        $r = Invoke-WebRequest -Uri "http://127.0.0.1:$Port/" -UseBasicParsing -TimeoutSec 1
        if ($r.StatusCode -eq 200) { $url = "http://127.0.0.1:$Port/" }
      } catch {}
    }
    if ($url) {
      $url | Set-Content $UrlFile -Encoding ascii
      Write-Host "    Ready: $url"
      return
    }
    $alive = Get-Process -Id ([int]$procId) -ErrorAction SilentlyContinue
    if (-not $alive) {
      Write-Host "ERROR: process exited before Ready"
      if (Test-Path $ErrLog) { Get-Content $ErrLog -Tail 20 }
      throw "start failed"
    }
    Start-Sleep -Milliseconds 200
  }
  throw "did not become ready within 30s"
}

function Build-App {
  Write-Host "==> Building frontend"
  Push-Location (Join-Path $Root "frontend")
  try {
    if (-not (Test-Path "node_modules")) { npm.cmd ci }
    npm.cmd run build
    if ($LASTEXITCODE -ne 0) { throw "frontend build failed" }
  } finally { Pop-Location }

  Write-Host "==> Building Go binary (sqlite_fts5)"
  $env:GOCACHE = if ($env:GOCACHE) { $env:GOCACHE } else { Join-Path $env:TEMP "session-insight-go-build" }
  go build -tags sqlite_fts5 -o $Bin .
  if ($LASTEXITCODE -ne 0) { throw "go build failed" }
  Write-Host "==> Build complete: $Bin"
}

function Show-Status {
  $p = Get-OwnedProcess
  if ($p) {
    Write-Host "running pid=$($p.Id) path=$($p.Path)"
    if (Test-Path $UrlFile) { Write-Host "url=$(Get-Content $UrlFile -Raw)" }
    try {
      $code = (Invoke-WebRequest -Uri "http://127.0.0.1:$Port/" -UseBasicParsing -TimeoutSec 2).StatusCode
      Write-Host "http=$code"
    } catch { Write-Host "http=down" }
  } else {
    Write-Host "not running"
  }
}

switch ($Command) {
  "build" { Build-App }
  "start" { Start-App }
  "stop" { Stop-App }
  "restart" { Stop-App; Start-App }
  "status" { Show-Status }
  "all" { Build-App; Start-App }
}
