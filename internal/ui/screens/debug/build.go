package debug

import (
	"runtime"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/buildinfo"
)

// collectBuildInfo reads AC-1's build & code fields. This is the single
// call site in this package that touches internal/foundation/buildinfo's
// package vars — a source scan for a hardcoded version/commit literal
// (GR#15's verification step) has nothing to find here: every field
// below is either read from buildinfo (itself -ldflags-injected, "dev"
// by default) or from runtime.Version() (the compiled-in toolchain
// string, likewise never hand-maintained).
func collectBuildInfo() BuildInfo {
	return BuildInfo{
		Version:      buildinfo.Version,
		Commit:       buildinfo.Commit,
		Branch:       buildinfo.Branch,
		BuildTimeUTC: buildinfo.BuildTime,
		GoVersion:    runtime.Version(),
		// BuildHost: see the BuildInfo doc comment in types.go — not yet
		// exposed by buildinfo/build.ps1, a known upstream gap.
		BuildHost:          "",
		BuildHostAvailable: false,
	}
}
