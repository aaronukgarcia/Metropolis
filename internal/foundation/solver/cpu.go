package solver

import (
	"errors"
	"fmt"
)

// ErrNotImplemented is returned by CPUBackend.Solve for every
// ProblemKind except EchoProblem, until the corresponding engine module
// lands its real algorithm.
var ErrNotImplemented = errors.New("solver: cpu backend has no implementation for this problem kind yet")

// CPUBackend is the mandatory, always-available fallback: it Supports
// every known ProblemKind so a Registry with CPUBackend registered never
// returns ErrNoFallback. Real algorithms (traffic assignment, cold-pass
// batch, deep projection, life writing) are not implemented yet — they
// arrive with their respective engine modules — with one deliberate
// exception: EchoProblem, which CPUBackend solves for real. EchoProblem
// is a trivial, fully-deterministic transform that proves the registry /
// fallback / determinism plumbing works end-to-end before any real
// solver exists.
type CPUBackend struct {
	// Name is the backend name reported in Response.Backend.
	Name string
}

// NewCPUBackend constructs a CPUBackend with the conventional name
// "cpu.v1".
func NewCPUBackend() *CPUBackend {
	return &CPUBackend{Name: "cpu.v1"}
}

// Supports always returns true: CPU is the mandatory local-fallback tier
// for every problem kind (see registry.go's ErrNoFallback doc comment).
func (c *CPUBackend) Supports(problem ProblemKind) bool {
	return true
}

// Solve dispatches to the real EchoProblem transform, or returns
// ErrNotImplemented for every other (real, not-yet-built) ProblemKind.
func (c *CPUBackend) Solve(req Request) (Response, error) {
	if req.Problem != EchoProblem {
		return Response{}, fmt.Errorf("%w: problem=%s", ErrNotImplemented, req.Problem)
	}
	return c.solveEcho(req)
}

// solveEcho deterministically transforms req.Payload: it XORs every byte
// against a keystream derived from req.Seed via splitmix64. This makes
// the output depend on both Payload and Seed exactly as the determinism
// contract (contract.go) requires: the same request produces
// byte-identical output every time, on any machine, and the transform is
// trivially checkable in tests without pulling in any real solver logic.
//
// splitmix64 is used purely as a tiny, dependency-free, deterministic
// keystream generator for this test/plumbing path — it is NOT a claim
// about RNG quality for real simulation use. Real engine code uses the
// counter-based Philox-style streams specified in M0-ENG §1.2.
func (c *CPUBackend) solveEcho(req Request) (Response, error) {
	out := make([]byte, len(req.Payload))
	x := req.Seed
	for i, b := range req.Payload {
		x += 0x9E3779B97F4A7C15
		z := x
		z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
		z = (z ^ (z >> 27)) * 0x94D049BB133111EB
		z = z ^ (z >> 31)
		out[i] = b ^ byte(z)
	}
	return Response{
		Payload: out,
		Backend: c.Name,
		Stats:   SolveStats{Iterations: 1},
	}, nil
}
