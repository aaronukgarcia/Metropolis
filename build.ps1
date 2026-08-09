<#
.SYNOPSIS
    Builds cmd/metropolis with build identity injected via -ldflags
    (M0-ENG §3: version/commit/branch/timestamp — NEVER hand-maintained).

.DESCRIPTION
    Module key: foundation.repo (see code.json)
    Spec ref:   M0-ENG §5; A8; M0-ENG §3 (build info)

    Mirrors the incantation documented in
    internal/foundation/buildinfo/buildinfo.go.

.PARAMETER DetGate
    FEAT-004 / GR#21: run the determinism gate's CI-facing test locally
    instead of the normal build (TestDeterminismGate,
    internal/engine/detgate/gate_test.go — same seed, 120 months, run
    twice at POOL-SIM=1 then again at POOL-SIM=14, sha256(worldSnapshot)
    must match). Mirrors exactly what .github/workflows/ci.yml's
    determinism-gate job runs, so a red result locally means CI would
    also be red — per Rule #21 (docs/golden-rules-detail.md), that is
    automatically P0.
#>

param(
    [switch]$DetGate
)

$ErrorActionPreference = "Stop"

if ($DetGate) {
    Write-Host "Running the determinism gate (FEAT-004, GR#21)..."
    go test ./internal/engine/detgate/ -run TestDeterminismGate -count=1 -v
    if ($LASTEXITCODE -ne 0) {
        Write-Error "DETERMINISM GATE RED (exit $LASTEXITCODE): automatically P0 per GR#21 (docs/golden-rules-detail.md Rule #21). Reverting the offending commit is always an acceptable first response."
        exit $LASTEXITCODE
    }
    Write-Host "Determinism gate green."
    exit 0
}

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
