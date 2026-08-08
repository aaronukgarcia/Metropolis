package serialize

import "io"

// Record is one opaque state document. The serializer does not know or care
// about domain types — Data is an arbitrary JSON document (object, array,
// scalar) exactly as produced by the caller; Kind is a caller-defined label
// ("citizen", "building", "command", ...) that lets a reader dispatch to the
// right decoder without the serializer itself knowing any engine types.
// Engine modules register their own record kinds elsewhere; this package
// only ever moves bytes plus a label.
type Record struct {
	// Kind labels the record for the reader's dispatch table. Never
	// interpreted by this package.
	Kind string

	// Data is the raw JSON document for this record. It is written
	// verbatim (as a json.RawMessage) — never re-marshalled — so byte-for-
	// byte determinism only depends on the caller producing stable bytes.
	Data []byte
}

// ShardMeta describes one shard: its name, the (nominal) record kind it
// carries by convention, and integrity/size metadata filled in by
// WriteShard once the shard has been fully written. A shard is normally
// homogeneous (one Kind) but the format does not enforce that — Kind here
// is metadata for humans and tooling, not a constraint checked per-record.
type ShardMeta struct {
	// Name is the shard's identifier, e.g. "citizens.0042". Combined with
	// the bundle layout this becomes the on-disk file name
	// (shards/<Name>.ndjson.gz for the NDJSON encoding).
	Name string

	// Kind is the nominal record kind carried by this shard.
	Kind string

	// RecordCount is the number of records written to (or read from) the
	// shard. Filled in by WriteShard; informational on ReadShard callers'
	// side (this package does not cross-check it against the header).
	RecordCount int64

	// ByteSize is the size in bytes of the encoded shard file as written
	// to the underlying io.Writer (i.e. the compressed size for the
	// NDJSON+gzip encoding — the size of the bytes that hash to SHA256).
	ByteSize int64

	// SHA256 is the lowercase hex SHA-256 digest of the exact bytes
	// written to the shard's io.Writer. Computed while writing (never a
	// second pass) so WriteShard never has to buffer a whole shard.
	SHA256 string

	// Encoding names the wire encoding, e.g. "ndjson+gzip". Bundle
	// validation and metctl use this to pick the right StateSerializer
	// implementation when reading a shard back.
	Encoding string
}

// RecordSource is a pull-style streaming iterator: each call returns the
// next record. ok is false (with a zero Record and nil error) once the
// source is exhausted. A non-nil error aborts WriteShard immediately.
//
// Pull rather than push (a callback the caller invokes per record) is
// deliberate: it lets WriteShard control backpressure and keeps the whole
// pipeline — caller's data source, this package's encoder, gzip, the
// underlying file — streaming one record at a time. Nothing in this
// package ever holds more than one record's bytes in memory at once.
type RecordSource func() (rec Record, ok bool, err error)

// RecordHandler receives one record at a time while a shard is read. A
// non-nil error return aborts ReadShard immediately (propagated to the
// caller of ReadShard).
type RecordHandler func(Record) error

// StateSerializer is the pluggable read/write contract for one shard of
// save/fixture state. Implementations MUST stream in both directions —
// WriteShard must not buffer more than one Record and ReadShard must not
// load the whole shard into memory before invoking the handler — because
// shards are read and written far larger than comfortably fits in RAM at
// 100 M-citizen scale (§5.3).
//
// A StateSerializer implementation knows nothing about engine record
// types: Record.Data is opaque bytes in, opaque bytes out.
type StateSerializer interface {
	// WriteShard drains next until exhaustion, encoding each Record to w.
	// It returns meta with RecordCount, ByteSize, SHA256 and Encoding
	// filled in (meta's other fields — Name, Kind — are taken from the
	// meta argument and passed through unchanged). The caller is
	// responsible for persisting the returned ShardMeta into the bundle's
	// Header.ShardIndex.
	WriteShard(w io.Writer, meta ShardMeta, next RecordSource) (ShardMeta, error)

	// ReadShard streams records from r, invoking handle once per record in
	// the order they were written. It returns the first error encountered
	// (from decoding or from handle itself), or nil after r is fully
	// consumed.
	ReadShard(r io.Reader, handle RecordHandler) error
}
