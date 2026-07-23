# Detached start helper for Windows (called from run.sh / run.ps1).
# Prints the child PID on stdout; exits non-zero on failure.
#
# Uses WMI Win32_Process.Create so the process is parented by WmiPrvSE, not the
# calling shell. That survives agent/job-object cleanup that kills Start-Process
# children when the parent shell exits.
param(
  [Parameter(Mandatory = $true)][string]$BinPath,
  [Parameter(Mandatory = $true)][string]$WorkDir,
  [Parameter(Mandatory = $true)][string]$LogPath,
  [Parameter(Mandatory = $true)][string]$ErrLogPath,
  [string]$Port = "8080",
  [string]$DataDir
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $BinPath)) {
  Write-Error "binary not found: $BinPath"
  exit 1
}

# Truncate logs so Ready wait does not see a stale "listening" line.
"" | Set-Content -LiteralPath $LogPath -Encoding utf8
"" | Set-Content -LiteralPath $ErrLogPath -Encoding utf8

$launchCmd = Join-Path $WorkDir "session-insight-launch.cmd"
$dataLine = ""
if ($PSBoundParameters.ContainsKey('DataDir') -and $DataDir -and $DataDir.Trim().Length -gt 0) {
  $dataLine = "set `"SI_DATA_DIR=$DataDir`""
} else {
  $dataLine = "set `"SI_DATA_DIR=`""
}

# Batch carries env + redirects. Absolute paths; no dependence on caller cwd.
@(
  "@echo off"
  "set `"PORT=$Port`""
  $dataLine
  "cd /d `"$WorkDir`""
  "`"$BinPath`" >> `"$LogPath`" 2>> `"$ErrLogPath`""
) | Set-Content -LiteralPath $launchCmd -Encoding ascii

# WMI Create: process tree is owned by the WMI service host, not this shell.
$cmdLine = "cmd.exe /c `"$launchCmd`""
$created = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{
  CommandLine      = $cmdLine
  CurrentDirectory = $WorkDir
}
if ($null -eq $created -or $created.ReturnValue -ne 0) {
  # Fallback: Start-Process (may die with parent job in sandboxed agents).
  $env:PORT = $Port
  if ($PSBoundParameters.ContainsKey('DataDir') -and $DataDir -and $DataDir.Trim().Length -gt 0) {
    $env:SI_DATA_DIR = $DataDir
  } else {
    Remove-Item Env:SI_DATA_DIR -ErrorAction SilentlyContinue
  }
  $p = Start-Process -FilePath $BinPath `
    -WorkingDirectory $WorkDir `
    -WindowStyle Hidden `
    -PassThru `
    -RedirectStandardOutput $LogPath `
    -RedirectStandardError $ErrLogPath
  if ($null -eq $p) {
    Write-Error "failed to start process (WMI+$($created.ReturnValue), Start-Process null)"
    exit 1
  }
  Write-Output $p.Id
  exit 0
}

# Resolve the real binary PID (Create returns cmd.exe's PID).
$want = (Resolve-Path -LiteralPath $BinPath).Path
$procId = $null
for ($i = 0; $i -lt 50; $i++) {
  $hit = Get-CimInstance Win32_Process -Filter "name = 'session-insight.exe'" -ErrorAction SilentlyContinue |
    Where-Object { $_.ExecutablePath -and ($_.ExecutablePath -ieq $want) } |
    Select-Object -First 1
  if ($hit) {
    $procId = $hit.ProcessId
    break
  }
  Start-Sleep -Milliseconds 100
}

if (-not $procId) {
  Write-Error "process started but session-insight.exe PID not found"
  exit 1
}

Write-Output $procId
exit 0
