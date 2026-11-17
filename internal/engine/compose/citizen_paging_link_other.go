//go:build !unix && !windows

package compose

// linkUnsupported: no per-GOOS classification is wired for this platform. A
// link failure there is retried as transient and then refused as a config
// error (MET-G817), which is still fail-closed: there is never a
// create-then-write fallback.
func linkUnsupported(error) bool { return false }
