//go:build !linux

package compose

// pidLivenessKnown is false on every non-linux GOOS this project builds on
// (Windows dev boxes, this project's CI runners — .github/workflows/ci.yml
// runs windows-latest — and any other target). Go's stdlib has no portable
// "is this pid alive" primitive: os.FindProcess on Windows always succeeds
// regardless of whether the pid exists, and Windows pids are recycled
// aggressively, so even a real Windows-specific check (golang.org/x/sys/
// windows OpenProcess) would be a same-host-only heuristic with its own
// false-positive risk from pid reuse — not worth the added dependency for a
// platform this project does not deploy citizen paging to (Azure Container
// Apps runs Linux containers; see citizen_paging_pid_linux.go's own doc
// comment). processLooksAlive therefore always answers "cannot rule out
// alive" here — fail closed: a stale claim on this GOOS can only be cleared
// via the explicit Reclaim knob, never auto-detected as dead.
const pidLivenessKnown = false

// processLooksAlive always returns true (indeterminate, treated as "assume
// live") on this GOOS — see pidLivenessKnown's doc comment.
func processLooksAlive(pid int) bool {
	return true
}
