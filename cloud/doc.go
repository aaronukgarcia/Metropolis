// Package cloud implements the Azure tiers for Metropolis: durable Blob
// saves, solver offload (including Batch-style tuning sweeps and the
// citizen cold-pass), and cloud citizen shards — all behind the two seams
// the local path already implements, so the engine never forks on
// cloud-vs-local.
//
// Module key: cloud.azure (see code.json; GUID 2167acd8-4aad-4bde-8f02-ac12f3d08c20)
// Spec ref:   §15 (cloud path); A9 (cloud thresholds); GDD §15; docs/cloud.md;
//
//	docs/design/solver-contract.md
//
// # The governing constraint: same seams as local
//
// Every cloud capability exposes the identical contract the local path
// already implements, so the player experience is identical at every tier
// (A9): cloud is a latency/capacity win, never a different answer and
// never a dependency the game cannot run without.
//
//   - [AzureSolver] implements the frozen [solver.Solver] interface (the
//     int.solver seam) for solver offload, Batch-style parameter sweeps
//     (DeepProjection), and the citizen cold-pass (ColdPassBatch). Its
//     Solve returns byte-identical Payload to the local backend, with the
//     local backend as the mandatory, automatic fallback on any cloud
//     failure.
//   - [BlobStore] is the durable storage tier: it persists and restores the
//     exact bytes the local serializer produced (int.serializer's output),
//     byte-for-byte — a cloud copy of the checkpoint for durability, never
//     a second source of truth and never a re-encoding (GR#3).
//
// # Determinism (GR#21)
//
// Cloud offload runs the same pure, seeded solver function as local and
// returns byte-identical results for the same (Problem, Seed, Payload).
// Blob saves are byte-identical copies of the local serializer output, so
// a restore reproduces the exact checkpoint. No wall-clock time is read
// anywhere in this package: cut-over thresholds are explicit [Config]
// values (consumed from int.solver's A9 sizing constants, never
// re-hardcoded), and every retry/backoff is logical (the
// integration.Connection attempt counter), never time.Now() and never a
// context deadline.
//
// # Mandatory local fallback
//
// Cloud is strictly an accelerant. When a cloud transport fails, the
// solver backend transparently re-runs the identical seeded solve locally
// and returns the identical Payload (distinguishable only by the
// diagnostic Response.Backend). The local path is the degenerate
// always-connected case: a Config with Enabled=false (the zero value) is a
// pure local pass-through — the permanent headless stand-in required by
// GR#20 ("every module keeps a passing stub for life"), so the engine
// boots and ticks with cloud entirely absent (the v1 reality).
//
// # Resilience
//
// Each tier owns an [integration.Connection] (Reconnect + re-auth /
// name-lookup hooks, logical backoff, catch-up drain). The cloud tier
// owns no queue of its own — the caller's QueuedTransport absorbs bursts
// (see docs/planning/icd/cloud.azure.md §9).
//
// # Operational numbers
//
// This module carries no player-felt balance numbers. Its operational
// thresholds — the retry budget and the A9 citizen-shard cut-over — are
// placeholders pending Aaron's balance pass, carried as explicit [Config]
// fields whose zero values are the all-local degenerate case (cloud
// absent); the A9 population thresholds themselves are single-sourced in
// int.solver's sizing package (solver.ExceedsLocalCPUCeiling /
// LocalCitizenCeilingLow) and consumed here, never re-declared (GR#3).
// This package carries those placeholders in Go rather than a data file:
// no data/cloud.json exists on this branch, so a Config literal is the
// placeholder home until the data-file regime is introduced.
//
// # Error surface (GR#7)
//
// cloud.azure has no MET range of its own yet (ICD §8, OD-2). It surfaces
// int.solver's and int.serializer's foundation.errors surface for contract
// failures, and maps Azure transport / Blob failures (unreachable,
// throttled, quota, not-found) to foundation.integration's resilience
// codes — the only registered remote-integration codes that exist today.
// Every surfaced error is registry-sourced and carries a correlation ID;
// a Blob save that fails is a returned error, never a silent drop (GR#17).
package cloud
