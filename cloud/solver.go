package cloud

import (
	"sync/atomic"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/integration"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/solver"
)

// DefaultSolverPriority is the cloud tier's documented slot in the
// int.solver priority ladder (CPU 0 < GPU ≈50 < cloud ≈100). It is a
// convention constant, not balance data — see docs/design/solver-contract.md
// open question #4 for the (still-open) named-constant resolution on the
// int.solver side.
const DefaultSolverPriority = 100

// SolverTransport is the solver-offload transport seam: the fake-able
// stand-in for an Azure Batch / gRPC solver slot. A real implementation
// would ship a solver.Request to the cloud and return the response; the
// only contract that matters here is that, for a Deterministic request, it
// returns a Response.Payload byte-identical to what the local backend
// produces (GR#21).
//
// Implementations MUST be safe for concurrent use by multiple goroutines
// (solver.Solver's own contract) and MUST NOT read wall-clock time for
// anything that affects Response.Payload (ICD §7).
type SolverTransport interface {
	// Solve offloads one stateless request. The returned Response.Payload
	// must be byte-identical to the local backend's for the same
	// (Problem, Seed, Payload) with Deterministic=true.
	Solve(req solver.Request) (solver.Response, error)
}

// AzureSolver implements solver.Solver for the cloud tiers that route
// through the int.solver seam: solver offload, Batch-style parameter
// sweeps (DeepProjection), and the citizen cold-pass (ColdPassBatch). It
// holds a mandatory local fallback and delegates resilience to an
// integration.Connection.
//
// The type is safe for concurrent use: the local backend, transport, and
// Connection are immutable after NewAzureSolver, and the fallback counter
// is atomic — there is no mutex to copy (so no SEC-020 copy-guard hazard;
// use via pointer, as every constructor in this package returns).
type AzureSolver struct {
	name      string
	enabled   bool
	transport SolverTransport
	local     solver.Solver
	conn      *integration.Connection

	fallbacks atomic.Int64
}

// NewAzureSolver constructs a cloud solver backend. transport is the
// offload seam; local is the mandatory fallback (typically the CPU backend
// or a solver.Registry-resolved chain). cfg.Enabled == false yields a pure
// local pass-through — the permanent headless stand-in (GR#20).
func NewAzureSolver(cfg Config, transport SolverTransport, local solver.Solver) *AzureSolver {
	return &AzureSolver{
		name:      cfg.backendName(),
		enabled:   cfg.Enabled,
		transport: transport,
		local:     local,
		conn:      integration.NewConnection(cfg.connectionConfig()),
	}
}

// Register adds this backend to r under its configured name (default
// "azure.solver") at the given priority. Registration is explicit — never
// an init() — so the engine boots all-local by default and only gains the
// cloud tier when the composition root opts in (cloud is a config change,
// not a code path).
func (a *AzureSolver) Register(r *solver.Registry, priority int) error {
	return r.Register(a.name, a, priority)
}

// Supports delegates to the local fallback: the cloud tier offloads
// whatever the local backend can already answer — same seams as local.
func (a *AzureSolver) Supports(problem solver.ProblemKind) bool {
	return a.local.Supports(problem)
}

// Solve offloads req to the cloud transport, transparently falling back to
// the local backend on any cloud failure. For a Deterministic request the
// returned Payload is byte-identical regardless of which path ran (GR#21);
// the only observable difference is the diagnostic Response.Backend.
//
// A disabled tier (Config.Enabled == false) skips the transport entirely
// and answers locally — the all-local degenerate case. A failed offload
// never surfaces the transport error to the caller: it re-runs the
// identical seeded solve locally and returns that result instead (ICD §9).
func (a *AzureSolver) Solve(req solver.Request) (solver.Response, error) {
	if !a.enabled {
		return a.local.Solve(req)
	}

	var resp solver.Response
	err := a.conn.Attempt(func() error {
		r, e := a.transport.Solve(req)
		if e != nil {
			return e
		}
		resp = r
		return nil
	})
	if err == nil {
		// Diagnostic only — see solver.Response.Backend's contract comment.
		resp.Backend = a.name
		return resp, nil
	}

	// Cloud failed: mandatory local fallback. Same seed, same problem,
	// byte-identical Payload — only latency changes (ICD §7).
	a.fallbacks.Add(1)
	return a.local.Solve(req)
}

// Reconnect re-establishes the cloud connection (re-auth + name lookup via
// the configured ReconnectHooks) and drains any configured backlog, per
// integration.Connection. A cloud solver tier owns no queue of its own
// (ICD §9), so the drain is normally a no-op.
func (a *AzureSolver) Reconnect(name string) (integration.DrainStats, error) {
	return a.conn.Reconnect(name)
}

// State reports the underlying connection's current state (monitoring, §10).
func (a *AzureSolver) State() integration.ConnState { return a.conn.State() }

// Retries reports the underlying connection's current retry counter.
func (a *AzureSolver) Retries() int64 { return a.conn.Retries() }

// Fallbacks reports the local-fallback activation count — the critical
// monitoring signal of §10: a rising count means the cloud tier is not
// paying for itself.
func (a *AzureSolver) Fallbacks() int64 { return a.fallbacks.Load() }
