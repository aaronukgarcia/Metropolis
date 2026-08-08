// Package errs is the error registry, correlation-ID, and structured
// logging foundation (Golden Rule #7: every error MUST be created from the
// error registry, no exceptions; Golden Rule #1: aggressive error trapping
// — log, type, correlation ID, selectable display).
//
// Module key: foundation.errors (see code.json)
// Spec ref:   GR#1; GR#7; M0-ENG §3 (log tail)
//
// # The contract
//
// [New] and [Wrap] are the only legal error constructors in Metropolis
// (code.json inbound pattern for foundation.errors: "errs.New(code,
// correlationId, ctx) — the only legal error constructor"). Every code
// passed to them must already exist in the registry (data/errors.json,
// loaded once via [registry.go]); using an unregistered code never panics
// — it returns a well-formed MET-F003 "unregistered code" error instead,
// so a programming mistake degrades to a visible, loggable error rather
// than a crash. See data/errors.json's "ranges" section for the
// MET-<layer><NNN> code format and the foundation layer's reserved
// sub-ranges.
//
// Every constructed *E is automatically logged (errors are stored, never
// printed-and-lost) through the package-level sink configured by
// [SetSink]; with no sink configured, or when a configured sink fails to
// write, entries fall back to an in-memory 200-entry ring buffer readable
// via [Recent] — the primitive the F12 debug info panel's error tail
// (M0-ENG §3) will read.
//
// # Determinism note for engine callers
//
// Per M0-ENG §1.1, engine simulation logic must never call the wall
// clock directly. [SetClock] overrides the package-wide clock used for
// *E.Time, and every [Logger] carries its own injectable Now field
// (defaulting to time.Now). Engine code should inject sim-time into both
// before ticking; UI/tooling code can leave the defaults alone.
package errs
