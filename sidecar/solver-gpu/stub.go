package solvergpu

import (
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/solver"
)

// Stub is the permanent headless stand-in for the GPU sidecar (AC-2, GR#20
// "stub-forever"): it reports Supports honestly (the same offload subset as
// Backend) and returns a typed "sidecar unavailable" error from Solve, so
// the registry/fallback/determinism plumbing is fully exercisable with no
// CUDA driver, no GPU, and no worker process present — exactly as CPUBackend
// is the solver seam's own minimum implementation.
//
// The Stub is stateless; the zero value is usable and safe for concurrent
// use.
type Stub struct{}

// Supports delegates to the package's single offload-subset declaration.
func (Stub) Supports(problem solver.ProblemKind) bool {
	return supports(problem)
}

// Solve always fails with the typed "sidecar unavailable" registry error,
// letting int.solver's fallback chain drop through to CPUBackend (AC-5).
func (Stub) Solve(req solver.Request) (solver.Response, error) {
	return solver.Response{}, errs.New(errSidecarUnavailable, errs.NewCorrelationID(), map[string]any{
		"problem": req.Problem.String(),
	})
}

var _ solver.Solver = Stub{}
