// Package solvergpu is the Go-side GPU solver sidecar (module key cloud.gpu,
// MOD-068, FEAT-192 Tier D): the client that plugs the frozen int.solver
// Solver contract into a separate out-of-process GPU worker
// (solver-gpu.exe, C++/CUDA in production) so heavy seeded solver sweeps
// (traffic assignment, cold-pass batches) can run on an RTX-class card
// while the engine cannot tell CPU from GPU except by latency.
//
// Module key: cloud.gpu (see code.json)
// GUID:        a755e8b6-fd25-4843-90e8-f0da23e50726
// Spec ref:    M0-ENG §1 ("one seam, three muscles: CPU -> GPU sidecar -> cloud", "do not entangle CUDA with the engine"); A4/A9 (sizing); docs/design/solver-contract.md (INT-003, the frozen seam).
// ICD:         docs/planning/icd/cloud.gpu.md (FEAT-192 Tier D stub)
//
// # The load-bearing contract: bit-identical determinism (GR#21)
//
// For a Deterministic request the GPU worker's Response.Payload MUST be
// byte-identical to the local CPUBackend's for the same (Problem, Seed,
// Payload) — the fallback chain and the CI determinism gate both depend on
// it. This package enforces that today by delegating the worker's transform
// to the same solver algorithm the CPU backend runs (GR#3, one source of
// truth); the real CUDA worker would implement the same seeded algorithm
// independently and be gated by the byte-identity test in backend_test.go.
// Response.Backend/Stats/Warnings are diagnostic-only: they are never read
// back into a Request, hashed into a snapshot, or fed into another Solve's
// Seed/Payload (AC-8; int.solver contract.go's loop-closing ban).
//
// # CUDA is confined to this package (GR#20, US-4)
//
// No engine package imports this package, CUDA, gRPC stubs, or any SDK. The
// sidecar plugs int.solver's registry as one more backend — a
// registry.Register call, never a new engine call site. The only registered
// outbound edge is cloud.gpu -> int.solver (code.json inbound
// d20c26e9-5a1a-4628-9c93-f95938547f19); this package consumes the solver
// contract and the universal foundation.errors seam, nothing else.
//
// # Local fallback is mandatory (ICD §9, AC-5)
//
// The sidecar is strictly an accelerant, never a dependency. On any GPU
// failure — absent, crashed, unreachable, over the VRAM envelope — the
// backend falls back to the local CPU solver, which re-runs the identical
// seeded function and returns the identical bytes, distinguishable only by
// the diagnostic Response.Backend label. It never returns a nil Response, a
// panic, or a hung call.
//
// # Statelessness (deliberately no SEC-020 copy guard)
//
// Backend holds no sync.Mutex and no aliasable MUTABLE state: config fields
// are immutable after NewBackend and the only mutable state (current
// transport + monitoring counters) lives in atomic values shared by any
// copy, so a struct copy behaves identically to the original with none of
// the "two locks, one referent" hazard a mutex-holding type would carry.
// The copy-guard discipline that protects Registry/Connection/Logger does
// not apply here by design (ICD §3: "the sidecar is stateless").
//
// # Open decision (surfaced for Bill/Aaron, per ICD §12)
//
// AC-4 of docs/planning/acceptance/cloud.gpu.md declares the offload subset
// as {TrafficAssignment, ColdPassBatch} only, excluding EchoProblem. That
// subset is honoured (see supports), with EchoProblem ALSO declared so the
// byte-identity crown invariant is provable today on the one kind with a
// real deterministic algorithm — mirroring int.solver's own use of
// EchoProblem to prove its plumbing (AC-2/ES-2's "EchoProblem-adjacent
// plumbing"). If Bill rules Echo out of the declared set, the determinism
// test simply loses its only computable fixture until a real engine module
// lands TrafficAssignment — flagged rather than silently chosen.
package solvergpu
