# Thin Windows wrapper around Taskfile.yml (the single source of build logic).
# Uses an installed `task`, or runs the pinned version through `go run`.
#
#   powershell -ExecutionPolicy Bypass -File scripts/manage.ps1 <task> [VAR=value ...]
#   e.g. scripts/manage.ps1 build VERSION=1.2.3
param(
    [Parameter(Position = 0)][string]$Task = "default",
    [Parameter(ValueFromRemainingArguments = $true)][string[]]$Rest
)

$ErrorActionPreference = "Stop"
$TaskVersion = "v3.53.1"
Set-Location (Split-Path -Parent $PSScriptRoot)

if (-not (Get-Command gcc -ErrorAction SilentlyContinue)) {
    $searchPaths = @(
        [Environment]::GetEnvironmentVariable("Path", "User"),
        [Environment]::GetEnvironmentVariable("Path", "Machine"),
        "C:\msys64\mingw64\bin",
        "C:\mingw64\bin"
    )
    foreach ($entry in $searchPaths) {
        if (-not $entry) { continue }
        foreach ($dir in ($entry -split ';')) {
            $dir = $dir.Trim()
            if ($dir -and (Test-Path (Join-Path $dir "gcc.exe"))) {
                $env:PATH = "$dir;$env:PATH"
                break
            }
        }
        if (Get-Command gcc -ErrorAction SilentlyContinue) { break }
    }
}

$taskCmd = Get-Command task -ErrorAction SilentlyContinue
if ($taskCmd) {
    & $taskCmd.Source $Task @Rest
} else {
    & go run "github.com/go-task/task/v3/cmd/task@$TaskVersion" $Task @Rest
}
exit $LASTEXITCODE
