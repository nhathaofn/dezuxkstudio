param (
    [Parameter(Position=0)]
    [ValidateSet("dev", "build", "build:server", "test", "clean")]
    [string]$Command = "dev",

    [Parameter(Position=1)]
    [string]$Version = ""
)

$ErrorActionPreference = "Stop"
$RootDir = Split-Path -Parent $PSScriptRoot

# Dynamic Git metadata detection
$GitCommit = try { (git rev-parse --short HEAD 2>$null) } catch { "dev" }
if (-not $GitCommit) { $GitCommit = "dev" }

if (-not $Version) {
    $Version = try { (git describe --tags --always --dirty 2>$null) } catch { "1.0.0" }
    if (-not $Version) { $Version = "1.0.0" }
}

$BuildDate = (Get-Date).ToString("yyyy-MM-ddTHH:mm:ssZ")
$LdFlags = "-s -w -X 'main.AppName=DezuxkStudio' -X 'main.AppVersion=$Version' -X 'main.GitCommit=$GitCommit' -X 'main.BuildDate=$BuildDate'"

switch ($Command) {
    "dev" {
        Write-Host "[Manage] Launching DezuxkStudio Dev Environment..." -ForegroundColor Green
        Set-Location $RootDir
        wails dev
    }
    "build" {
        Write-Host "[Manage] Building Frontend..." -ForegroundColor Cyan
        Set-Location "$RootDir\frontend"
        npm run build

        Write-Host "[Manage] Building Desktop Client: DezuxkStudio-$Version.exe..." -ForegroundColor Cyan
        Set-Location $RootDir
        wails build -o "DezuxkStudio-$Version.exe" -platform windows/amd64 -ldflags "$LdFlags" -clean
        Write-Host "[Manage] Desktop build ready: build/bin/DezuxkStudio-$Version.exe" -ForegroundColor Green
    }
    "build:server" {
        Write-Host "[Manage] Building Go Headless Server: bin/dezuxk-server.exe..." -ForegroundColor Cyan
        Set-Location $RootDir
        if (-not (Test-Path "$RootDir\bin")) { New-Item -ItemType Directory -Path "$RootDir\bin" | Out-Null }
        go build -ldflags "$LdFlags" -o bin/dezuxk-server.exe ./cmd/server
        Write-Host "[Manage] Server binary ready: bin/dezuxk-server.exe" -ForegroundColor Green
    }
    "test" {
        Write-Host "[Manage] Running Go test suite..." -ForegroundColor Cyan
        Set-Location $RootDir
        go test -v ./...
    }
    "clean" {
        Write-Host "[Manage] Cleaning build outputs..." -ForegroundColor Yellow
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue "$RootDir\bin", "$RootDir\build\bin", "$RootDir\frontend\dist"
        Write-Host "[Manage] Clean completed." -ForegroundColor Green
    }
}
