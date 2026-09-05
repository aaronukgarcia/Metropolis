//go:build linux

package compose

import "syscall"

// pidLivenessKnown is true on linux: processLooksAlive below returns a real,
// same-host, same-PID-namespace answer via kill(pid, 0) (Go's
// syscall.Kill(pid, 0) sends no signal, only checks deliverability). This is
// the platform the project actually deploys to (Azure Container Apps runs
// Linux containers -- see CLAUDE.md's Docker/deploy notes), so a real
// liveness check is possible there, subject to the cross-container caveat
// documented on CitizenPagingOptions.Reclaim: a pid is only meaningful
// within the PID NAMESPACE of the container that recorded it. Two separate
// containers (e.g. two overlapping Container Apps revisions sharing one
// /data mount) each have their OWN pid 1, 2, 3... — a claim recorded by
// container A's pid 42 says NOTHING about whether container B currently has
// something running as ITS OWN pid 42. processLooksAlive therefore only
// ever helps the SAME-CONTAINER crash-restart case (a metroserve process
// that crashed and is restarting in the SAME container, PID namespace, and
// mount) -- the cross-container case is NOT distinguishable this way and
// always falls through to requiring an explicit Reclaim.
const pidLivenessKnown = true

// processLooksAlive reports whether pid appears to be a live, running
// process IN THIS CONTAINER'S PID NAMESPACE. false is trustworthy ("this
// exact pid does not exist here right now"); true is NOT proof the
// original claimant is still alive across a container boundary -- see this
// file's own doc comment.
func processLooksAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	// ESRCH ("no such process") is the only case this check treats as
	// confidently dead. Any other outcome (nil = alive/reachable; EPERM =
	// exists but owned by another user) is treated as "cannot rule out
	// alive" -- fail closed.
	err := syscall.Kill(pid, syscall.Signal(0))
	return err != syscall.ESRCH
}
