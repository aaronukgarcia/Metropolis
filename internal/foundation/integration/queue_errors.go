package integration

// Registry error codes for foundation.integration (FEAT-188, increment 2:
// the priority-tiered overflow queue). Range: F900-F919, claimed per
// docs/planning/acceptance/README.md's "per-module error sub-ranges are
// claimed at build time" convention. Checked against data/errors.json's
// "ranges.reserved" table AND `grep -rn "MET-F9" internal/ cmd/` before
// claiming (BUG-008's lesson that the table alone is not always current)
// — F900-F999 was entirely unclaimed; the MET-F900/F901/F902/F999
// literals that DO appear elsewhere in the tree live only inside
// internal/foundation/errs' own _test.go fixture files, which the
// source-scan gate (source_scan_test.go) excludes outright, so they
// carry no real registration and collide with nothing here.
//
// Every code below is registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test guards against this ever
// drifting out of sync.
const (
	// ErrT0QueueExhausted: a T0-critical command (proposal §3: "every
	// tick, must-not-drop") could not be enqueued because the T0 tier's
	// in-memory buffer is full. T0 has no disk fallback by design — its
	// whole contract is "never queued past the current tick," so
	// spilling it to disk (which can only be caught up on a LATER tick)
	// would violate that contract more than rejecting it outright.
	// Rejected explicitly to the caller (never silently dropped) — see
	// GR#17 / proposal §4 "backpressure, never silent drop."
	ErrT0QueueExhausted = "MET-F900"

	// ErrSpillWriteFailed: writing a T1-batchable command's overflow
	// segment to disk (queue_disk.go's writeSegment, atomic
	// staging-then-rename) failed — the command was never queued at
	// all. A registry error, not a silent drop, per the same GR#17
	// contract as ErrT0QueueExhausted.
	ErrSpillWriteFailed = "MET-F901"

	// ErrSpillReadFailed: a promoted T1 overflow segment could not be
	// read back or decoded (queue_disk.go's readSegment) while draining
	// — either a filesystem read failure or protocol.DecodeCommand
	// rejected its content as malformed. Because every segment reaches
	// its final path only via writeSegment's atomic rename, a decode
	// failure here means the file was corrupted or torn by something
	// OTHER than this package's own writer; the drain halts and
	// surfaces this rather than silently skipping the record or
	// fabricating a substitute command.
	ErrSpillReadFailed = "MET-F902"

	// ErrSpillDirCreateFailed: the T1 overflow segment directory (or
	// its .spill-staging subdirectory) could not be created.
	ErrSpillDirCreateFailed = "MET-F903"

	// ErrQueueTransportCopied: a *QueuedTransport method was called on
	// a struct copy of the value NewQueuedTransport returned —
	// SEC-020-class guard, mirrors protocol.InProcTransport's
	// checkNotCopied exactly (see queue.go's checkNotCopied doc
	// comment for the full rationale).
	ErrQueueTransportCopied = "MET-F904"
)
