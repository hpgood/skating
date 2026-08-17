# Skating CLI Installer for Windows
param(
    [string]$Version = "0.1.0"
)

$ErrorActionPreference = "Stop"
$InstallDir = "$env:USERPROFILE\.skating\bin"
$Binary = "skating.exe"

Write-Host "Installing skating v$Version for Windows..." -ForegroundColor Cyan

# Create install directory
New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null

# Build from source
Write-Host "Building from source..."
Push-Location $PSScriptRoot\..
try {
    go build -o "$InstallDir\$Binary" .\cmd\skating\
    Write-Host "Build successful" -ForegroundColor Green
} finally {
    Pop-Location
}

# Add to PATH
$UserPath = [Environment]::GetEnvironmentVariable("PATH", "User")
if ($UserPath -notlike "*$InstallDir*") {
    [Environment]::SetEnvironmentVariable("PATH", "$InstallDir;$UserPath", "User")
    $env:Path = "$InstallDir;$env:Path"
    Write-Host "Added skating to user PATH" -ForegroundColor Green
}

Write-Host "skating v$Version installed to $InstallDir\$Binary" -ForegroundColor Green
Write-Host "Run 'skating --version' to verify" -ForegroundColor Cyan