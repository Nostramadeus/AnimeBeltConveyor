# Builds abc.exe with its icon and version string. Needs only Go.
#   .\build.ps1                 (uses "go" from PATH)
#   $env:GO = 'C:\Go\bin\go.exe'; .\build.ps1   (when go is not on PATH)
# The icon resource (cmd/abc/rsrc_windows_amd64.syso) is rebuilt from
# assets/icon/abc.ico only when it is missing or older than the .ico. Both files
# are committed, so a plain "go build ./cmd/abc" also gets the icon.

$ErrorActionPreference = 'Continue'  # native tools write progress to stderr; exit codes are checked instead
Set-Location -LiteralPath $PSScriptRoot

$go = 'go'
if ($env:GO) { $go = $env:GO }
if (-not (Get-Command $go -ErrorAction SilentlyContinue)) {
    Write-Error "Go not found. Install Go, or set `$env:GO to the full path of go.exe."
    exit 1
}

$ico = 'assets\icon\abc.ico'
$syso = 'cmd\abc\rsrc_windows_amd64.syso'
if (Test-Path -LiteralPath $ico) {
    $stale = -not (Test-Path -LiteralPath $syso)
    if (-not $stale) { $stale = (Get-Item -LiteralPath $ico).LastWriteTimeUtc -gt (Get-Item -LiteralPath $syso).LastWriteTimeUtc }
    if ($stale) {
        Write-Host "icon: $ico -> $syso"
        & $go run github.com/akavel/rsrc@v0.10.2 -ico $ico -arch amd64 -o $syso
        if ($LASTEXITCODE -ne 0) { Write-Error 'rsrc failed'; exit 1 }
    }
} else {
    Write-Host "icon: $ico not found, building without an icon"
}

$version = 'dev'
if (Get-Command git -ErrorAction SilentlyContinue) {
    $d = & git describe --tags --always --dirty 2>$null
    if ($LASTEXITCODE -eq 0 -and $d) { $version = "$d".Trim() }
}

& $go build -ldflags "-X main.version=$version" -o abc.exe ./cmd/abc
if ($LASTEXITCODE -ne 0) { Write-Error 'go build failed'; exit 1 }
Write-Host "built abc.exe ($version)"
# abcw.exe: the same program without a console window (quiet start; logs go to abc.log).
& $go build -ldflags "-X main.version=$version -H windowsgui" -o abcw.exe ./cmd/abc
if ($LASTEXITCODE -ne 0) { Write-Error 'go build (abcw) failed'; exit 1 }
Write-Host "built abcw.exe ($version, no console window)"
