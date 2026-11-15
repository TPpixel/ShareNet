$ErrorActionPreference = 'Stop'

$root = $PSScriptRoot
$workspace = Split-Path $root -Parent
$go = Join-Path $env:LOCALAPPDATA "Programs\Go\bin\go.exe"
if (-not (Test-Path $go)) {
    $cmd = Get-Command go -ErrorAction SilentlyContinue
    if ($cmd) { $go = $cmd.Source }
}
if (-not (Test-Path $go)) { throw "Go compiler not found at $go" }

# Locate source runtime assets
$ptCandidates = @(
    (Join-Path $workspace "adb-no-admin\platform-tools"),
    (Join-Path $workspace "ShareNetUSB\tools\platform-tools"),
    "$env:LOCALAPPDATA\Android\Sdk\platform-tools"
)
$ptSource = $ptCandidates | Where-Object { Test-Path (Join-Path $_ "adb.exe") } | Select-Object -First 1
if (-not $ptSource) { throw "platform-tools (adb.exe) not found" }

$gnCandidates = @(
    (Join-Path $workspace "gnirehtet\gnirehtet-rust-win64"),
    (Join-Path $workspace "ShareNetUSB\tools\gnirehtet")
)
$gnSource = $gnCandidates | Where-Object { (Test-Path (Join-Path $_ "gnirehtet.exe")) -and (Test-Path (Join-Path $_ "gnirehtet.apk")) } | Select-Object -First 1
if (-not $gnSource) { throw "gnirehtet.exe / gnirehtet.apk not found" }

$hpCandidates = @(
    (Join-Path $workspace "hotspot-proxy-app\build\hotspot-proxy.apk"),
    (Join-Path $workspace "ShareNetUSB\tools\hotspot-proxy\hotspot-proxy.apk")
)
$hpSource = $hpCandidates | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $hpSource) { throw "hotspot-proxy.apk not found" }

$sbCandidates = @(
    (Join-Path $workspace "HotspotVPN-Win\sing-box.exe"),
    (Join-Path $workspace "ShareNetUSB\tools\sing-box\sing-box.exe")
)
$sbSource = $sbCandidates | Where-Object { Test-Path $_ } | Select-Object -First 1
if (-not $sbSource) { throw "sing-box.exe not found" }

# Pack embedded runtime tools into internal/embedded/tools.zip
$zipPath = Join-Path $root "internal\embedded\tools.zip"
$zipTemp = "$zipPath.tmp"
if (Test-Path $zipTemp) { Remove-Item $zipTemp -Force }

Add-Type -AssemblyName System.IO.Compression
Add-Type -AssemblyName System.IO.Compression.FileSystem
$fs = [System.IO.File]::Create($zipTemp)
$archive = New-Object System.IO.Compression.ZipArchive($fs, [System.IO.Compression.ZipArchiveMode]::Create, $false)
try {
    $packList = @(
        @{ Src = (Join-Path $ptSource "adb.exe");          Entry = "tools/platform-tools/adb.exe" },
        @{ Src = (Join-Path $ptSource "AdbWinApi.dll");    Entry = "tools/platform-tools/AdbWinApi.dll" },
        @{ Src = (Join-Path $ptSource "AdbWinUsbApi.dll"); Entry = "tools/platform-tools/AdbWinUsbApi.dll" },
        @{ Src = (Join-Path $gnSource "gnirehtet.exe");    Entry = "tools/gnirehtet/gnirehtet.exe" },
        @{ Src = (Join-Path $gnSource "gnirehtet.apk");    Entry = "tools/gnirehtet/gnirehtet.apk" },
        @{ Src = $hpSource;                                Entry = "tools/hotspot-proxy/hotspot-proxy.apk" },
        @{ Src = $sbSource;                                Entry = "tools/sing-box/sing-box.exe" }
    )
    foreach ($item in $packList) {
        if (-not (Test-Path $item.Src)) { throw "Missing asset: $($item.Src)" }
        [System.IO.Compression.ZipFileExtensions]::CreateEntryFromFile(
            $archive, $item.Src, $item.Entry, [System.IO.Compression.CompressionLevel]::Optimal
        ) | Out-Null
    }
}
finally {
    $archive.Dispose()
    $fs.Dispose()
}
Move-Item -Path $zipTemp -Destination $zipPath -Force
Write-Host ("Packed embedded runtime: {0:N1} MB" -f ((Get-Item $zipPath).Length / 1MB)) -ForegroundColor Cyan

# Build standalone single ShareNet.exe with embedded Win11 icon + asInvoker manifest (.syso)
$distDir = Join-Path $root "dist"
New-Item -ItemType Directory -Force -Path $distDir | Out-Null
$oldDistSub = Join-Path $distDir "ShareNetUSB"
if (Test-Path $oldDistSub) { Remove-Item $oldDistSub -Recurse -Force }
$exePath = Join-Path $distDir "ShareNet.exe"

Write-Host "Compiling single-file ShareNet.exe (Win11 style + no admin)..." -ForegroundColor Cyan
Push-Location $root
try {
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    & $go run ./cmd/mkres
    if ($LASTEXITCODE -ne 0) { throw "mkres failed with $LASTEXITCODE" }
    & $go mod tidy
    if ($LASTEXITCODE -ne 0) { throw "go mod tidy failed with $LASTEXITCODE" }
    & $go build -trimpath -ldflags="-H windowsgui -s -w" -o $exePath .
    if ($LASTEXITCODE -ne 0) { throw "Go compilation failed with $LASTEXITCODE" }
}
finally {
    Pop-Location
}

$rootPortable = Join-Path $workspace "ShareNetUSB"
New-Item -ItemType Directory -Force -Path $rootPortable | Out-Null
Get-Process -Name ShareNet, gnirehtet, adb -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 300
foreach ($stale in @("ShareNetUSB.exe", "ShareNetUSB_Start.bat", "tools")) {
    $p = Join-Path $rootPortable $stale
    if (Test-Path $p) { Remove-Item $p -Recurse -Force -ErrorAction SilentlyContinue }
}
Copy-Item -Path $exePath -Destination (Join-Path $rootPortable "ShareNet.exe") -Force

Write-Host ("SUCCESS: Single standalone ShareNet.exe built ({0:N1} MB) at {1}" -f ((Get-Item $exePath).Length / 1MB), $rootPortable) -ForegroundColor Green
