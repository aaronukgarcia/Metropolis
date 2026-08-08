// Package serialize implements the StateSerializer contract and the
// save/fixture schema (NDJSON shards, binary at scale). The save format IS
// the fixture format — one serialisation to rule them all (M0-ENG §2,
// H-REPLAY).
//
// Module key: int.serializer (see code.json)
// Spec ref:   A3; §5.3; V.2.2; M0-ENG §2.2
package serialize
