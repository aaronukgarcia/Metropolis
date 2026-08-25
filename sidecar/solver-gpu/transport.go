package solvergpu

import "github.com/aaronukgarcia/Metropolis/internal/foundation/solver"

// Transport is the sidecar's gRPC wire seam to the out-of-process GPU
// worker (solver-gpu.exe, C++/CUDA in production). The Backend talks ONLY
// to this interface — never to CUDA, a gRPC stub, or any SDK directly — so
// the wire can be a real gRPC client in production and an in-process double
// in tests/headless runs (AC-3's "fake/mock SolverService"; US-4's "CUDA
// confined to the sidecar").
//
// The wire contract is int.solver's field-for-field (AC-3): one unary solve
// over solver.Request -> solver.Response, with no extra fields and no
// dropped fields. Transport moves bytes; it never reinterprets Payload.
type Transport interface {
	// SolveRemote sends req to the worker and returns its response. A
	// non-nil error means the worker was unreachable, timed out, or
	// rejected the request — the Backend maps that to a registry error and
	// falls back to the local solver (mandatory local fallback, ICD §9).
	SolveRemote(req solver.Request) (solver.Response, error)
}

// localTransport is the in-process stand-in for the GPU worker: it runs the
// SAME pure seeded solver function as the local CPU backend by delegating to
// a solver.Solver (GR#3 — one source of truth for the transform; a real
// CUDA worker implements the same algorithm independently and is gated by
// the byte-identity test in backend_test.go) and relabels the response's
// diagnostic Backend to the worker's name. This is what proves the
// "byte-identical to CPU" crown invariant today: the offload path and the
// local path produce the same bytes, differing only in the diagnostic
// Backend label (AC-6/AC-8).
type localTransport struct {
	backendName string
	worker      solver.Solver
}

// newLocalTransport builds the default in-process worker, delegating to the
// mandatory CPU backend for the transform.
func newLocalTransport(name string) *localTransport {
	return &localTransport{backendName: name, worker: solver.NewCPUBackend()}
}

// SolveRemote implements Transport. Backend is diagnostic-only
// (contract.go); relabelling it here can never affect determinism, only the
// F12 panel's backend label.
func (t *localTransport) SolveRemote(req solver.Request) (solver.Response, error) {
	resp, err := t.worker.Solve(req)
	if err != nil {
		return solver.Response{}, err
	}
	resp.Backend = t.backendName
	return resp, nil
}

// Hooks is the reconnect seam (ICD §9): name lookup and re-authentication,
// re-established when the Backend reconnects after a transport failure. The
// in-process degenerate case (noopHooks) is the always-connected path; a
// real gRPC deployment injects real service-discovery + credential hooks.
// Both methods must be deterministic given their inputs (GR#21) — no
// wall-clock timeout, no random jitter.
type Hooks interface {
	// Lookup resolves the sidecar's logical name to an address/handle.
	Lookup(name string) (string, error)
	// Authenticate re-establishes credentials for addr.
	Authenticate(addr string) error
}

// noopHooks is the always-connected, no-op Hooks for the in-process case —
// the degenerate "local = always connected" path (ICD §9). It mirrors
// integration.LocalReconnectHooks' shape without importing the integration
// package, whose edge is not registered for cloud.gpu (see doc.go).
type noopHooks struct{}

// Lookup always succeeds, returning name unchanged (no separate addressing
// scheme for the in-process case).
func (noopHooks) Lookup(name string) (string, error) { return name, nil }

// Authenticate always succeeds (no remote credential to re-establish).
func (noopHooks) Authenticate(string) error { return nil }

var _ Hooks = noopHooks{}
