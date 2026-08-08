<#
.SYNOPSIS
    Builds cmd/metropolis with build identity injected via -ldflags
    (M0-ENG §3: version/commit/branch/timestamp — NEVER hand-maintained).

.DESCRIPTION
    Module key: foundation.repo (see code.json)
    Spec ref:   M0-ENG §5; A8; M0-ENG §3 (build info)

    Mirrors the incantation documented in
    internal/foundation/buildinfo/buildinfo.go.
#>

$ErrorActionPreference = "Stop"

$pkg = "github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"

$version = (git describe --tags --always --dirty 2>$null)
if (-not $?) { $version = "dev" }

$commit = (git rev-parse HEAD 2>$null)
if (-not $?) { $commit = "dev" }

$branch = (git rev-parse --abbrev-ref HEAD 2>$null)
if (-not $?) { $branch = "dev" }

$buildTime = (Get-Date).ToUniversalTime().ToString("o")

$ldflags = "-X $pkg.Version=$version -X $pkg.Commit=$commit -X $pkg.Branch=$branch -X $pkg.BuildTime=$buildTime"

Write-Host "Building cmd/metropolis..."
Write-Host "  Version:   $version"
Write-Host "  Commit:    $commit"
Write-Host "  Branch:    $branch"
Write-Host "  BuildTime: $buildTime"

go build -ldflags $ldflags -o metropolis.exe ./cmd/metropolis
if ($LASTEXITCODE -ne 0) {
    Write-Error "go build failed (exit $LASTEXITCODE)"
    exit $LASTEXITCODE
}

Write-Host "Built metropolis.exe"
