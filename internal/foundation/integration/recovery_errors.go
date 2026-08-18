package integration

// Registry error codes for foundation.integration's crash-recovery replay
// (recovery.go) and its durable Write-Ahead Log (wal.go). Range:
// F900-F919 (see queue_errors.go's header comment for the full claim
// story); increment 2 used F900-F904, resilience.go (increment 3, part
// 1) used F905-F907, so this file claims F908-F914. Checked against
// data/errors.json's "ranges.reserved" table AND `grep -rn "MET-F9"
// internal/ cmd/` before claiming (BUG-008's lesson) — F908-F914 were
// unclaimed (F908-F910 were previously claimed by this file's FIRST,
// destructive-review-REJECTED version of recovery.go, which reused the
// T1 overflow queue as the durable log; F909's wording below has been
// updated in place to describe the WAL instead — see wal.go/recovery.go's
// header comments for why the design changed. F908/F910 keep their
// original meaning: a checkpoint-load failure and an apply failure are
// still exactly the same shape of error under the corrected design).
//
// Every code below is registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test guards against this ever
// drifting out of sync.
const (
	// ErrRecoveryCheckpointLoadFailed: Recover could not read the active
	// checkpoint's head pointer or could not load its bundle back into
	// the supplied participants (checkpoint.Manager.CurrentID/Load). A
	// crash-recovery attempt that cannot even establish its base state
	// must fail loudly, never silently fall back to "start from empty
	// world" (which would look like ordinary boot on a corrupted/missing
	// checkpoint root, hiding real data loss).
	ErrRecoveryCheckpointLoadFailed = "MET-F908"

	// ErrWALReadFailed: a WAL entry (or the WAL-CURRENT slot pointer)
	// that DOES exist at its final, atomically-renamed/promoted path
	// failed to read or decode. Per wal.go's own doc comment, an entry
	// only ever reaches that path fully written — so a read/decode
	// failure on a PRESENT file means genuine corruption from something
	// outside this package's own writer, never a legitimate torn write
	// (those are simply ABSENT from the directory listing — see
	// listWALSeqs' doc comment — and are never this error).
	ErrWALReadFailed = "MET-F909"

	// ErrRecoveryApplyFailed: a caller-supplied apply function
	// (Recover's third argument) returned an error while replaying a
	// decoded command from the WAL. Recovery halts immediately rather
	// than continuing past a failed apply — replaying entry N+1 on top of
	// a state that never actually received entry N would silently
	// produce a rebuilt state that is NOT byte-identical to the pre-crash
	// state, defeating the entire point of this package (GR#21).
	ErrRecoveryApplyFailed = "MET-F910"

	// ErrWALDirCreateFailed: os.MkdirAll failed for a WAL slot's
	// directory or its .wal-staging subdirectory (wal.go's
	// writeWALEntry/writeCurrentSlot). No entry was appended and no
	// prune progressed as a result of this failure.
	ErrWALDirCreateFailed = "MET-F911"

	// ErrWALWriteFailed: the atomic staging-write-then-rename of a WAL
	// entry, or of the WAL-CURRENT pointer file, failed (disk full,
	// permissions, or another filesystem error). The entry/pointer was
	// never durably written at all — per GR#17 this surfaces as a
	// registry error rather than a silent drop; a command whose Append
	// returns this error MUST NOT be applied (wal.go's "seam" note —
	// applying an unlogged command reintroduces the commit-before-apply
	// race this whole package exists to close).
	ErrWALWriteFailed = "MET-F912"

	// ErrWALPruneFailed: WAL.Prune could not finish rebuilding the
	// inactive slot with the retained (post-checkpoint) entries, or could
	// not flip WAL-CURRENT to it. The WAL-CURRENT pointer is only ever
	// flipped after every retained entry is durably written (wal.go's
	// "Atomic writes + atomic prune" section), so this failure always
	// leaves the PREVIOUSLY active slot still active and still fully
	// intact — a failed prune loses no data, only the space it would
	// have reclaimed.
	ErrWALPruneFailed = "MET-F913"

	// ErrWALCopied: a *WAL method was called on a struct copy of the
	// value NewWAL returned — SEC-020-class guard, mirrors
	// QueuedTransport.checkNotCopied/Connection.checkNotCopied exactly:
	// WAL carries both a sync.Mutex VALUE and an aliasable field set
	// (root/correlationID/slot strings, nextSeq) a struct copy would
	// alias while gaining its own independent, non-exclusive mutex.
	ErrWALCopied = "MET-F914"
)
