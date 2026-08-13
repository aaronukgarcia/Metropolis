package save

// Registry error codes for feat.saveux. Range: E800-E899, claimed here
// per docs/planning/acceptance/README.md's "per-module error sub-ranges
// are claimed at build time" convention. Checked against
// data/errors.json's "ranges.reserved" table AND
// `grep -rn "MET-E8" internal/ cmd/` before claiming (BUG-008's lesson
// that the table alone is not always current) — no prior MET-E8xx code
// existed either place; E700-E799 (engine.helper) was the last-claimed
// E-layer sub-range.
//
// Every code below is registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test guards against this ever
// drifting out of sync.
const (
	// ErrSaveInProgress: a save trigger arrived while another save was
	// already in flight on the same *Manager (AC-11). The single-save-
	// in-flight guard rejects the second trigger with this typed,
	// loggable outcome rather than queuing it silently or interleaving
	// shard writes.
	ErrSaveInProgress = "MET-E800"

	// ErrStagingCreateFailed: the staging bundle directory
	// (root/.staging/<random>/) could not be created — a filesystem-
	// level failure before any participant has been asked for a
	// record.
	ErrStagingCreateFailed = "MET-E801"

	// ErrParticipantWriteFailed: a registered Participant's
	// RecordSource returned an error, or the underlying shard write
	// failed, while staging a save. The staged directory is discarded;
	// nothing already-promoted (AC-9/AC-13) is affected.
	ErrParticipantWriteFailed = "MET-E802"

	// ErrHeaderWriteFailed: serialize.WriteHeader failed against the
	// staged bundle directory.
	ErrHeaderWriteFailed = "MET-E803"

	// ErrMetaWriteFailed: this package's own save-meta.json sidecar
	// (AC-6) could not be written to the staged bundle directory.
	ErrMetaWriteFailed = "MET-E804"

	// ErrStagedValidationFailed: the staged bundle failed
	// serialize.ValidateBundle after every shard/header/meta write —
	// AC-9's gate before promotion. The staged directory is discarded
	// and NOT renamed into a discoverable name.
	ErrStagedValidationFailed = "MET-E805"

	// ErrPromotionFailed: the final os.Rename from the staging
	// directory to the bundle's discoverable name failed (e.g. a name
	// collision, or a filesystem error). The staged directory is left
	// in place under root/.staging for forensic inspection rather than
	// silently deleted, since this is an unexpected failure mode, not
	// the ordinary "validation failed" path.
	ErrPromotionFailed = "MET-E806"

	// ErrFormatVersionMismatch: Load/LoadLatest encountered a bundle
	// whose Header.FormatVersion has a different MAJOR than this
	// build's serialize.CurrentFormatVersion (AC-12). Wraps
	// serialize.CheckFormatVersion's own error; never a generic or
	// swallowed failure.
	ErrFormatVersionMismatch = "MET-E807"

	// ErrMetaReadFailed: this package's save-meta.json sidecar could
	// not be read or decoded for a bundle List/Load is inspecting.
	ErrMetaReadFailed = "MET-E808"

	// ErrBundleNotFound: Load was asked to load a bundle directory that
	// does not exist or is not a valid bundle (header.json missing).
	ErrBundleNotFound = "MET-E809"

	// ErrNoValidSaveFound: LoadLatest walked every entry in the
	// relevant save history and none of them validated — every
	// candidate was corrupted/truncated (AC-10's exhausted-history
	// case).
	ErrNoValidSaveFound = "MET-E810"

	// ErrListFailed: List could not enumerate a save root's
	// manual/autosave/milestone subdirectories (a filesystem-level
	// failure, not a per-bundle validation problem — a per-bundle
	// header/meta read failure is reported per-entry, not as this
	// code).
	ErrListFailed = "MET-E811"

	// ErrUnknownParticipantKind: Load found a shard in a bundle's
	// ShardIndex whose Kind has no matching registered Participant on
	// this Manager — the registry that wrote the bundle and the
	// registry loading it back have drifted apart.
	ErrUnknownParticipantKind = "MET-E812"

	// ErrShardReadFailed: a registered Participant's Handler returned
	// an error, or the underlying shard decode failed, while loading a
	// bundle.
	ErrShardReadFailed = "MET-E813"

	// ErrBundleValidationFailed: Load's initial serialize.ValidateBundle
	// call against dir failed for a reason OTHER than a FormatVersion
	// major mismatch (which gets its own, more specific
	// ErrFormatVersionMismatch) — e.g. a SHA256/size mismatch, a missing
	// header, a shard path that is a directory instead of a file, or a
	// semantically-bogus header field. Wraps ValidateBundle's own error
	// so the original diagnostic detail is never lost (GR#7 — this was
	// previously returned bare/unwrapped, the Destructive round's REJECT
	// finding on this item).
	ErrBundleValidationFailed = "MET-E814"
)
