package invariant

// Registry error codes for engine.invariant (MOD-019). Range: E300-E399,
// declared in data/errors.json's "ranges.reserved" table. Every code
// below IS registered there with real severity/module/message/remedy
// fields (GR#7). The internal/foundation/errs source-scan test guards
// against this ever drifting out of sync again, and against another
// module's range accidentally overlapping this one (BUG-008's root
// cause — checked against data/errors.json's reserved table AND
// `grep -rn "MET-E3" internal/ cmd/` before claiming, per that lesson).
const (
	// ErrConservationViolation: RunSuite reported a Detected Violation
	// for one or more registered invariants. Constructed (and therefore
	// logged, per foundation.errors' construct()) for every violation in
	// both dev and release builds (AC-9: release mode's "registry-sourced
	// logged error" IS this code; dev mode logs the same way before its
	// additional hard assert).
	ErrConservationViolation = "MET-E300"

	// ErrWiringAfterSeal: Wire/WireDaily was called after the target
	// core.Engine had already sealed (core.ErrEngineSealed, MET-E011) —
	// AC-7b. Wrapped rather than swallowed: the caller sees this
	// package's own correlation ID AND, via errors.Unwrap, the original
	// core.ErrEngineSealed cause, so a caller-ordering mistake (wiring
	// dispatched after the engine's first AdvanceTicks call) is loud,
	// not a silently-never-running invariant suite.
	ErrWiringAfterSeal = "MET-E301"

	// ErrRegistryCopied: a *Registry method was called on a struct copy
	// of the value NewRegistry returned — the same SEC-014/SEC-016/
	// SEC-020 shape guarded elsewhere in this codebase (Engine.self,
	// registry.Registry.self, protocol's transport types). See
	// Registry.self's doc comment (registry.go).
	ErrRegistryCopied = "MET-E302"

	// ErrNilInvariant: Registry.Register was called with a nil Invariant.
	ErrNilInvariant = "MET-E303"

	// ErrDuplicateInvariant: Registry.Register was called twice with an
	// Invariant reporting the same Name() — rejected rather than
	// silently overwriting the earlier registration (mirrors
	// foundation.registry.Register's duplicate-key behaviour).
	ErrDuplicateInvariant = "MET-E304"
)
