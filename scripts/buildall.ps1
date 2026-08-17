param(
    [string]$Version = "0.2.0"
)

$date = Get-Date -Format "yyyy-MM-dd"
$commit = (git rev-parse --short HEAD 2>$null) -replace "`n", ""
if (-not $commit) { $commit = "none" }

$ldflags = "-s -w -X main.version=$Version -X main.commit=$commit -X main.date=$date"

function Build-Target {
    param($os, $arch, $suffix)
    Write-Host "Building $os $arch ..." -ForegroundColor Cyan
    $env:GOOS = $os
    $env:GOARCH = $arch
    $out = "build/skating-$os-$arch$suffix"
    go build -ldflags="$ldflags" -o $out ./cmd/skating/
    if ($LASTEXITCODE -eq 0) {
        Write-Host "  -> $out" -ForegroundColor Green
    } else {
        Write-Host "  -> FAILED" -ForegroundColor Red
    }
}

Remove-Item -Recurse -Force build -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Force build | Out-Null

Build-Target -os linux   -arch amd64 -suffix ""
Build-Target -os linux   -arch arm64 -suffix ""
Build-Target -os darwin  -arch amd64 -suffix ""
Build-Target -os darwin  -arch arm64 -suffix ""
Build-Target -os windows -arch amd64 -suffix ".exe"
Build-Target -os windows -arch arm64 -suffix ".exe"

Write-Host "`nDone. Version: $Version  Commit: $commit  Date: $date" -ForegroundColor Green