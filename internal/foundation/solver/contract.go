package solver

// Solver is the offload seam itself: a stateless request/response contract
// that lets heavy computation run on CPU (v1, always available), a GPU
// sidecar, or Azure cloud, with the engine unable to tell the difference
// except by latency (M0-ENG §1, GDD §15).
//
// Determinism is the entire point of this contract. For a Deterministic
// request (v1: always true, see Request.Deterministic) the Response
// Payload MUST be bit-identical regardless of which backend produced it.
// Response.Backend and Response.Stats are diagnostic-only: they exist for
// the F12 info panel and failover logging, and MUST NEVER be read by
// simulation logic, folded into world state, mixed into an RNG stream, or
// hashed into the determinism-gate snapshot (M0-ENG §1.2). If simulation
// code ever finds itself branching on Response.Backend, that is a
// contract violation, not a feature.
type Solver interface {
	// Solve executes a single stateless request and returns a response.
	// Implementations MUST NOT retain req.Payload after returning, MUST
	// NOT consult wall-clock time for anything that affects
	// Response.Payload (SolveStats.ElapsedMS is the sole, explicit
	// exception — see SolveStats), and MUST be safe for concurrent use by
	// multiple goroutines.
	Solve(req Request) (Response, error)

	// Supports reports whether this backend can handle the given problem
	// kind. The registry (registry.go) uses it to filter candidates: a
	// backend that returns false here is never selected for that
	// ProblemKind, even if registered.
	Supports(problem ProblemKind) bool
}

// Request is the versioned, backend-agnostic input to Solve. It is
// intentionally opaque at this layer: Payload's shape is defined per
// ProblemKind (see problems.go) and versioned independently via
// SchemaVersion, so a backend can be upgraded without breaking older
// engine builds mid-rollout.
type Request struct {
	// Problem selects which offload slot this request targets.
	Problem ProblemKind

	// SchemaVersion is the version of Payload's schema for Problem. Bump
	// it whenever a ProblemKind's payload struct changes shape — schema
	// versioning is independent of the app's GR#2 version bump.
	SchemaVersion uint32

	// Seed feeds any pseudo-randomness the solve needs (e.g. stochastic
	// user equilibrium's route-choice perturbation, §19). It is part of
	// the determinism contract: same Problem + same Seed + same Payload
	// (bytes) ⇒ same Response.Payload (bytes), on every backend.
	Seed uint64

	// Deterministic MUST be true in v1 — every backend the engine talks
	// to today is required to produce bit-identical output for a given
	// (Problem, Seed, Payload). The field exists so the schema does not
	// need to change the day a backend legitimately wants to offer a
	// non-deterministic fast-approximate mode (e.g. a surrogate model,
	// GDD §15's "surrogate-model path" for subsystems that outgrow local
	// compute). Until that day, Solver implementations SHOULD reject
	// Deterministic=false with a typed error rather than silently
	// honouring it — see the design doc's open questions for the exact
	// error contract, which is deferred until a non-deterministic backend
	// actually exists.
	Deterministic bool

	// Payload is the problem-specific request body, encoded per Problem's
	// versioned schema (see problems.go). The solver contract itself
	// never interprets these bytes — that is left to each backend's
	// implementation of the given ProblemKind.
	Payload []byte
}

// Response is the versioned, backend-agnostic output of Solve.
type Response struct {
	// Payload is the problem-specific result body. For a Deterministic
	// request this MUST be byte-identical across backends. That property
	// is what the CI determinism gate and the fallback chain both rely
	// on: a caller that fails over from GPU to CPU mid-game must never
	// see a different answer, only a different latency.
	Payload []byte

	// Backend names the backend that produced this response (e.g.
	// "cpu.v1", "gpu.sidecar", "azure.batch"). DIAGNOSTIC ONLY — for
	// logging and the F12 info panel. Simulation code MUST NOT branch on
	// this value or let it influence world state.
	Backend string

	// Stats carries wall-clock performance diagnostics. DIAGNOSTIC ONLY,
	// see SolveStats for the determinism carve-out this field requires.
	Stats SolveStats

	// Warnings are non-fatal, human-readable notes (e.g. "equilibrium hit
	// MaxIterations before ConvergenceEpsilon"). Never machine-parsed by
	// simulation logic — if a warning needs to change simulation
	// behaviour, it belongs in Payload instead, versioned like everything
	// else.
	Warnings []string
}

// SolveStats carries wall-clock performance diagnostics about a Solve
// call. This is the ONE place in the solver contract where a
// time.Now()-derived measurement is permitted at all (M0-ENG §1.1:
// "Nothing in the engine ever calls wall-clock time for logic") — because
// these fields are diagnostic-only: never read back into simulation
// state, never hashed into the determinism-gate snapshot, and never used
// to decide anything about Response.Payload's content. A backend
// implementation may populate ElapsedMS from time.Now()/time.Since; the
// solver contract package itself never calls either.
type SolveStats struct {
	// ElapsedMS is wall-clock milliseconds spent inside Solve, for the
	// F12 panel and failover logging. Diagnostic only — MUST NOT feed
	// simulation state.
	ElapsedMS int64

	// Iterations is the number of solver iterations performed (e.g. the
	// equilibrium inner-loop pass count, §19). Diagnostic only — do not
	// use it to seed or gate simulation logic; put user-facing
	// convergence notes in Response.Warnings instead.
	Iterations int
}
