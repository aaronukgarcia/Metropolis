// Package serialize implements the StateSerializer contract and the
// save/fixture schema (NDJSON shards, binary at scale). The save format IS
// the fixture format — one serialisation to rule them all (M0-ENG §2,
// H-REPLAY).
//
// The canonical implementation ([NDJSONSerializer]) writes sharded,
// gzip-compressed NDJSON (one JSON object per line): streamable in both
// directions so headless tools can process saves without loading whole
// worlds into memory (§5.3). A versioned binary shard format is reserved
// ([BinarySerializer]) for the day profiling shows JSON marshalling on the
// snapshot path is the bottleneck (A3) — it is not implemented yet and its
// methods return a clear "not implemented" error.
//
// [Header] is the small JSON file bundled alongside the shards
// (see [Bundle]) that carries format version, world seed, sim tick,
// AppVersion, the sticky DebugTouched flag (§14, M0-ENG §3), and the shard
// index (name, kind, count, size, hash, encoding per shard).
//
// Module key: int.serializer (see code.json)
// Spec ref:   A3; §5.3; V.2.2; M0-ENG §2.2
package serialize
