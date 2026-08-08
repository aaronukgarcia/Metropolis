package serialize

import (
	"fmt"
	"io"
)

// errNotImplementedCode is a placeholder for the eventual registry-sourced
// error code. foundation/errs (the error registry, F010-F099) is not
// necessarily merged yet, so BinarySerializer cannot depend on it — GR#7
// still applies once it can.
//
// TODO(foundation.errors): once internal/foundation/errs lands, register
// this under the F300-F399 range reserved for foundation.serialize in
// data/errors.json (see that file's "reserved" table) and switch every use
// below from fmt.Errorf(errNotImplementedCode+...) to
// errs.New(codeConstant, correlationID, ctx).
const errNotImplementedCode = "MET-F300"

// BinarySerializer is the RESERVED, NOT YET IMPLEMENTED binary
// StateSerializer for saves above the (not yet chosen) NDJSON size
// threshold (A3, R2). Every method returns a not-implemented error; it
// exists so the type is nameable and so calling code can select a
// serializer by ShardMeta.Encoding without a type switch on a type that
// doesn't exist yet.
//
// Intended design, for whenever profiling of the amortised cold pass
// (§5.3, A1/A2) shows JSON marshalling — not the tick path, which never
// touches serialization, but the background snapshot path — is the
// bottleneck at 100 M-citizen scale:
//
//   - A versioned, little-endian binary shard format. The version lives
//     in the shard (or is carried by the bundle Header, TBD at design
//     time) so old binary shards remain readable across format bumps
//     using the same major/minor/patch discipline as [Header].
//   - Per-record layout mirrors the columnar struct-of-arrays cold
//     citizen store (A1): field-level compression (bucketed enums,
//     delta-coded ages, bit-packed states), not a naive
//     encoding/gob-style dump of Go structs — the win is the same
//     60-100 B/citizen target that motivates SoA storage in memory.
//   - Still shard-streamable: WriteShard/ReadShard keep the same
//     never-buffer-the-whole-shard contract as NDJSONSerializer. The
//     binary format's whole reason to exist is throughput, so it must
//     not regress the streaming property that makes headless tools able
//     to process saves without loading whole worlds.
//   - metctl export remains the lossless escape hatch: given any binary
//     bundle, it reads via BinarySerializer.ReadShard and re-writes via
//     NDJSONSerializer.WriteShard, so JSON stays the project's universal
//     accessibility format even once a hot path stops using it directly
//     (R2's "JSON stays the lingua franca... not universal wire format").
//   - Arrives when profiling demands it (A3) — not before. Until then
//     NDJSON+gzip is canonical for every save and fixture regardless of
//     size.
type BinarySerializer struct{}

// WriteShard is not implemented. See the BinarySerializer doc comment for
// the intended design and data/errors.json's F300-F399 range for where its
// error codes will live once foundation/errs is available.
func (BinarySerializer) WriteShard(_ io.Writer, meta ShardMeta, _ RecordSource) (ShardMeta, error) {
	return meta, fmt.Errorf("%s: BinarySerializer.WriteShard not implemented (reserved for A3, arrives when profiling demands it; shard %q)", errNotImplementedCode, meta.Name)
}

// ReadShard is not implemented. See the BinarySerializer doc comment for
// the intended design and data/errors.json's F300-F399 range for where its
// error codes will live once foundation/errs is available.
func (BinarySerializer) ReadShard(_ io.Reader, _ RecordHandler) error {
	return fmt.Errorf("%s: BinarySerializer.ReadShard not implemented (reserved for A3, arrives when profiling demands it)", errNotImplementedCode)
}
