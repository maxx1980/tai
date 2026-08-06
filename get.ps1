<#
.SYNOPSIS
  Installs webssh on Windows by way of WSL.

.DESCRIPTION
  There is no native Windows build of webssh yet — internal/pty has no ConPTY
  backend, so the web terminal cannot start a shell outside WSL. This script
  makes sure WSL and a Linux distro are present (installing them if not),
  then runs the normal get.sh installer inside that distro.

.PARAMETER Distro
  Which WSL distro to install/use. Default: Ubuntu.

.PARAMETER Run
  Start webssh immediately after installing it.

.PARAMETER GetShArgs
  Extra arguments passed through to get.sh inside WSL, e.g. "--ui browser".

.EXAMPLE
  irm https://raw.githubusercontent.com/maxx1980/tai/main/get.ps1 | iex

.EXAMPLE
  .\get.ps1 -Run

.EXAMPLE
  .\get.ps1 -GetShArgs "--ui browser"
#>

param(
    [string]$Distro = "Ubuntu",
    [switch]$Run,
    [string]$GetShArgs = ""
)

$ErrorActionPreference = "Stop"
# PowerShell 7.3+ treats a native command's stderr output as a terminating
# error whenever it also exits non-zero, honoring $ErrorActionPreference
# above — exactly what wsl.exe does when WSL isn't installed yet, which is
# the one case this script most needs to handle instead of crashing on. Turn
# that off; exit codes are checked explicitly below. A harmless no-op on
# versions without this preference variable (Windows PowerShell 5.1, PS <7.3).
$PSNativeCommandUseErrorActionPreference = $false

function Test-IsAdmin {
    $id = [Security.Principal.WindowsIdentity]::GetCurrent()
    (New-Object Security.Principal.WindowsPrincipal($id)).IsInRole(
        [Security.Principal.WindowsBuiltinRole]::Administrator)
}

function Test-WslReady {
    # wsl.exe writes to stderr and exits non-zero when WSL isn't installed;
    # $PSNativeCommandUseErrorActionPreference above keeps that from being
    # treated as a terminating error, so it's safe to just check the exit code.
    $null = & wsl.exe --status 2>$null
    return $LASTEXITCODE -eq 0
}

function Get-WslDistros {
    $out = & wsl.exe -l -q 2>$null
    if ($LASTEXITCODE -ne 0) { return @() }
    # Windows PowerShell 5.1 misreads wsl.exe's UTF-16LE console output and
    # interleaves NUL bytes between characters; strip them before trimming.
    return $out | ForEach-Object { ($_ -replace "`0", "").Trim() } | Where-Object { $_ -ne "" }
}

if (-not (Get-Command wsl.exe -ErrorAction SilentlyContinue)) {
    Write-Error "wsl.exe was not found. This needs Windows 10 build 1607+ (2004+ recommended) — update Windows and try again."
    exit 1
}

if (-not (Test-WslReady)) {
    if (-not (Test-IsAdmin)) {
        Write-Host "WSL is not installed yet, and installing it needs an administrator prompt."
        Write-Host "Open PowerShell as Administrator and run this script again."
        exit 1
    }
    Write-Host "Installing WSL and $Distro (this can take a few minutes)..."
    & wsl.exe --install -d $Distro
    Write-Host ""
    Write-Host "WSL was just installed. Windows usually needs a reboot before it can run it."
    Write-Host "Reboot, then run this script again to finish installing webssh."
    exit
}

$distros = @(Get-WslDistros)
if ($distros.Count -eq 0) {
    if (-not (Test-IsAdmin)) {
        Write-Host "WSL has no Linux distro yet, and installing one needs an administrator prompt."
        Write-Host "Open PowerShell as Administrator and run this script again."
        exit 1
    }
    Write-Host "WSL is present but has no Linux distro yet. Installing $Distro..."
    & wsl.exe --install -d $Distro
    Write-Host "Installed. Run this script again to continue installing webssh."
    exit
}

if ($distros -notcontains $Distro) {
    $Distro = $distros[0]
    Write-Host "Using existing distro '$Distro' instead of the default."
}

Write-Host "WSL is ready ($Distro). Installing webssh inside it..."

# -u root sidesteps the interactive first-launch username/password wizard a
# brand-new distro would otherwise show, and webssh needs nothing a non-root
# user would give it that root does not already have.
$installCmd = "curl -fsSL https://raw.githubusercontent.com/maxx1980/tai/main/get.sh | bash"
if ($GetShArgs) { $installCmd = "$installCmd -s -- $GetShArgs" }
& wsl.exe -d $Distro -u root -- bash -lc $installCmd
if ($LASTEXITCODE -ne 0) {
    Write-Error "webssh install failed inside WSL (exit $LASTEXITCODE)."
    exit 1
}

Write-Host ""
Write-Host "webssh is installed inside WSL ($Distro). To start it:"
Write-Host "  wsl -d $Distro -u root -- webssh --no-open"
Write-Host ""
Write-Host "--no-open matters here: webssh's default 'app' mode looks for a"
Write-Host "chromium-based browser to open, and a bare WSL distro has none —"
Write-Host "you don't need to install one there. WSL2 forwards localhost to"
Write-Host "Windows automatically, so once it prints a URL like"
Write-Host "http://127.0.0.1:8022/?token=..., open it in whatever browser"
Write-Host "you already have on Windows."

if ($Run) {
    & wsl.exe -d $Distro -u root -- webssh --no-open
}
