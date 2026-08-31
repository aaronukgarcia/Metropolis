// Package persist is the durable persistence seam for FEAT-1972079936
// Phase 1 (compute-offload epic, "Path A"). It exists to close the
// documented durability gap in internal/harness/replay.Recorder
// (record.go: "buffers records in memory only and loses them on crash")
// and in feat.checkpoint's snapshot story, without either of those
// packages depending on this one yet — that wiring is a LATER increment
// owned by a different lane. This increment (inc1 of the phase) ships
// only the standalone abstraction: the Store interface, a local-disk
// implementation, and the cross-process rehydrate proof.
//
// # Scope discipline
//
// This package is a clean leaf: it imports nothing from
// internal/engine, internal/protocol, internal/harness, or
// internal/foundation/serialize. Journal records and snapshot bundles
// are opaque []byte payloads as far as Store is concerned — the caller
// (the engine's journaler seam, or checkpoint.Manager, in a later
// increment) owns their encoding. Keeping the payload type opaque here
// means:
//
//   - Zero new cross-module edges are needed to land this increment
//     (GR#25) — the edges the acceptance doc's "Architect" section
//     flags (engine.core -> persist, feat.checkpoint -> persist,
//     cmd/metroserve -> persist) are all deferred to the wiring
//     increment, when a concrete adapter (e.g. one that marshals
//     serialize.Record) is introduced in the CALLING package.
//   - This package can be fully unit-tested in isolation with no sim
//     state, no engine bootstrap, and no protocol wire format at all.
//
// # Determinism (GR#21)
//
// Store never influences sim computation — it is a pure sink/source.
// No method here does anything the sim's tick loop could observe as
// nondeterministic: no wall-clock is load-bearing, and every directory
// listing this package returns is explicitly sorted before being
// handed back (never a bare map-range or an unsorted os.ReadDir result
// treated as "the order"), per the map-range-with-break class of bug
// this project has hit before (Vestige metropolis-map-range-break-gotcha).
//
// # Concurrency model
//
// Phase 2's design is one session (hence one writer) per city, so this
// package documents and enforces "single-writer-per-city": DiskStore
// serializes concurrent AppendJournal/PutSnapshot calls for the SAME
// CityKey via a per-key mutex (safe, not a race), but does not attempt
// to give concurrent writers to the same city snapshot isolation
// beyond "each individual append/put is atomic and none corrupt the
// store" — a second concurrent writer to the same city is a caller bug
// this package tolerates without corrupting on-disk state, not a
// supported access pattern.
package persist
