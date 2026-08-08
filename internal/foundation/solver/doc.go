// Package solver defines the solver contract: the CPU/GPU/cloud
// interchangeable offload seam (CPU v1 always works -> GPU sidecar local
// acceleration -> Azure cloud). One interface, three backends; the engine
// cannot tell them apart except by latency (M0-ENG §1: "the same solver
// interface will have three interchangeable backends ... One seam, three
// muscles. Do not entangle CUDA with the engine.").
//
// Module key: int.solver (see code.json)
// Spec ref:   §15; A4; A9; M0-ENG §1
//
// # Files
//
//   - contract.go  — the Solver interface, Request/Response, and the
//     determinism rule that makes the whole seam trustworthy: for
//     Deterministic requests, Response.Payload must be bit-identical no
//     matter which backend produced it. Backend/Stats are diagnostic-only.
//   - problems.go  — ProblemKind, the four known offload slots (§15/A4:
//     TrafficAssignment, ColdPassBatch, DeepProjection, LifeWriting), and
//     their versioned payload schema stubs.
//   - sizing.go    — the A4/A9 capacity-planning tables as typed constants
//     and estimator helpers (OD matrix bytes, road graph bytes, GPU VRAM
//     envelope, local-CPU citizen ceiling).
//   - registry.go  — backend registration, priority ordering, and the
//     mandatory local-fallback rule: Get fails loudly if no backend
//     supports a problem kind, and transparently falls back to the next
//     candidate if a higher-priority backend errors.
//   - cpu.go       — CPUBackend, the mandatory always-available fallback.
//     Registered for all four problem kinds; real algorithms arrive with
//     their engine modules. EchoProblem is the one kind it solves for
//     real, to prove the plumbing end-to-end.
//
// # Why this package has zero engine/UI knowledge
//
// This package imports nothing from internal/ (aside from, optionally,
// buildinfo) and knows nothing about traffic, citizens, or projections
// beyond the shape of their payload stubs. That is the point: it is the
// seam, not a participant. engine.traffic, engine.citizens and friends
// depend on solver; solver never depends on them.
//
// See docs/design/solver-contract.md for the freeze-review write-up:
// seam diagram, schema, determinism/fallback rules, sizing tables, the
// gRPC mapping sketch for the sidecar, and open questions.
package solver
