# Detached start helper for Windows (called from run.sh).
# Prints the child PID on stdout; exits non-zero on failure.
param(
  [Parameter(Mandatory = $true)][string]$BinPath,
  [Parameter(Mandatory = $true)][string]$WorkDir,
  [Parameter(Mandatory = $true)][string]$LogPath,
  [Parameter(Mandatory = $true)][string]$ErrLogPath,
  [string]$Port = "8080",
  # Optional; omit or pass a non-empty path. Empty string is treated as unset
  # (PowerShell cannot bind -DataDir "" as a string parameter).
  [string]$DataDir
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path -LiteralPath $BinPath)) {
  Write-Error "binary not found: $BinPath"
  exit 1
}

$env:PORT = $Port
if ($PSBoundParameters.ContainsKey('DataDir') -and $DataDir -and $DataDir.Trim().Length -gt 0) {
  $env:SI_DATA_DIR = $DataDir
} else {
  Remove-Item Env:SI_DATA_DIR -ErrorAction SilentlyContinue
}

# Truncate logs so Ready wait does not see a stale "listening" line.
"" | Set-Content -LiteralPath $LogPath -Encoding utf8
"" | Set-Content -LiteralPath $ErrLogPath -Encoding utf8

$p = Start-Process -FilePath $BinPath `
  -WorkingDirectory $WorkDir `
  -WindowStyle Hidden `
  -PassThru `
  -RedirectStandardOutput $LogPath `
  -RedirectStandardError $ErrLogPath

if ($null -eq $p) {
  Write-Error "Start-Process returned null"
  exit 1
}

Write-Output $p.Id
exit 0
