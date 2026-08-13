// Package buildinfo carries the build identity (version, commit, branch,
// build timestamp) that the F12 debug info panel and CLI --version output
// must never hand-maintain (M0-ENG §3: "all injected via -ldflags at
// build; NEVER hand-maintained"). M0-ENG §3's F12 "Build & code" row also
// lists the build host machine name (FEAT-034).
//
// Module key: foundation.buildinfo (see code.json)
// Spec ref:   M0-ENG §3 (build info); M0-ENG §5; A8
//
// The variables below default to "dev" and are overwritten at link time via
// -ldflags -X. build.ps1 computes the values from git (and the local
// hostname) and passes exactly:
//
//	go build -ldflags "-X github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo.Version=$version -X github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo.Commit=$commit -X github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo.Branch=$branch -X github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo.BuildTime=$buildTime -X github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo.Host=$buildHost" ./cmd/metropolis
//
// Where (PowerShell):
//
//	$version   = git describe --tags --always --dirty
//	$commit    = git rev-parse HEAD
//	$branch    = git rev-parse --abbrev-ref HEAD
//	$buildTime = (Get-Date).ToUniversalTime().ToString("o")
//	$buildHost = $env:COMPUTERNAME
package buildinfo

// Version is the git describe output (tag, or short hash + -dirty if the
// tree has uncommitted changes) at build time. "dev" when built without
// build.ps1 (e.g. `go build` / `go run` directly, or `go test`).
var Version = "dev"

// Commit is the full git commit hash at build time.
var Commit = "dev"

// Branch is the git branch name at build time.
var Branch = "dev"

// BuildTime is the UTC build timestamp in RFC3339 format.
var BuildTime = "dev"

// Host is the hostname of the machine the binary was built on (M0-ENG §3
// F12 "Build & code" row: "build host"). FEAT-034.
var Host = "dev"

// String renders a single human-readable build identity line, e.g. for
// --version output and the F12 info panel build & code row.
func String() string {
	return "metropolis " + Version + " (" + Commit + ", " + Branch + ", built " + BuildTime + ", host " + Host + ")"
}
