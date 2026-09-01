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

	// ErrPriorHeaderReadFailed: writeBundle detected an existing bundle
	// at finalDir (BUG-157 — this call is a save-over) but could not
	// read that bundle's on-disk header.json to carry its DebugTouched
	// flag forward via serialize.Header.MergeDebugTouched (the same
	// SEC-024/SEC-027 sticky-flag hygiene SEC-027 enforced one layer up
	// in engine.core's Snapshot). The save is aborted rather than
	// proceeding and risking a debug-touched save silently coming back
	// clean.
	ErrPriorHeaderReadFailed = "MET-E815"

	// ErrReservedSaveName: SaveManual was called with a name matching
	// (or containing) the internal ".replaced-stage-<random>" marker
	// bundle.go's writeBundle uses to tag a crash-stranded displaced
	// sibling (BUG-158's replacedSuffixRe/isReplacedSiblingName/
	// replacedSiblingGlob). BUG-159: that marker pattern was never
	// actually unreachable from player input -- SaveManual's name
	// parameter had zero validation, so a name that happened to end in
	// (or contain) the literal marker text would either be permanently
	// hidden by List's filter or, worse, cause reapDisplacedSiblings to
	// glob-match and RemoveAll an unrelated real save on a later save to
	// a prefix-matching slot. Rejected at SaveManual's entry point,
	// before any filesystem write, so the collision can never occur.
	ErrReservedSaveName = "MET-E816"

	// ErrUnsafeSaveName: SaveManual was called with a name that is unsafe
	// to join, unmodified, into a save bundle path -- BUG-160, a genuine
	// arbitrary-directory-write vulnerability, worse in kind than
	// BUG-159's marker-collision gap: name flowed straight into
	// filepath.Join(root, "manual", name) with NO filepath.Clean/IsAbs/
	// ".." rejection at all, so a name such as "../../evil-escaped-<root
	// basename>" wrote a full valid save bundle OUTSIDE the configured
	// save root entirely. Also covers BUG-161's follow-up finding: names
	// that were technically safe to join into a path but degenerate
	// enough that they should never reach real filesystem I/O either --
	// other C0 control characters (tab, newline, BEL, ESC, backspace,
	// etc, not just NUL), an empty-or-whitespace-only name, and an
	// overlong name (see maxSaveNameLen). Rejected at SaveManual's entry
	// point (before isReservedSaveName's marker check, and before any
	// filesystem call) by isUnsafeSaveName (bundle.go): empty (or
	// whitespace-only after trimming), ".", "..", longer than
	// maxSaveNameLen, any name containing a path separator ('/' or '\',
	// checked for both regardless of build GOOS) or a drive-letter/ADS
	// colon anywhere in it, or any name containing a C0 control
	// character (byte 0x00-0x1F, including but not limited to NUL).
	ErrUnsafeSaveName = "MET-E817"

	// ErrManagerCopied: a *Manager method was called on a struct copy of
	// the value NewManager returned (SEC-020-class — astgate's live-tree
	// scan flagged writeBundleLocked as an unguarded mutex-hazard
	// candidate: Manager has both mu sync.Mutex and aliasable reference
	// fields (participants []Participant), exactly the shape every other
	// checkNotCopied guard in this codebase protects). checkNotCopied
	// (manager.go) rejects every guarded call before mu is ever touched,
	// mirroring Engine.checkNotCopied/Registry.checkNotCopied/
	// Store.checkNotCopied/Screen.checkNotCopied exactly (GR#3).
	ErrManagerCopied = "MET-E818"

	// ErrSaveSeedMismatch: Load/LoadAt found the bundle's Header.WorldSeed
	// does not match the loading composition's own world seed (BUG-479).
	// save.Context.WorldSeed is written into every bundle's header
	// (writeBundleLocked, save.go), and every module Participant's saved
	// shard omits its own `seed` field on the claim "reproduced from
	// save.Context.WorldSeed" — but until this code existed, nothing
	// actually enforced that the loading composition's seed was the one
	// the bundle was written from. Loading a bundle into a differently-
	// seeded composition therefore silently diverged every seed-derived
	// stateless draw (attract hash draws, det.Stream per-draw RNG) from
	// the saved trajectory, with no error at all.
	//
	// Raised by Load BEFORE any participant Handler is invoked (the
	// header is fully decoded by serialize.ValidateBundle ahead of the
	// per-shard loop — see load.go), so a refused load leaves every
	// Participant, and therefore the whole composition, byte-for-byte
	// untouched. Never raised when the caller passes AllowSeedMismatch()
	// (LoadOption, options.go) — the explicit opt-in for a deliberate
	// reseed, e.g. the FEAT-1972079897 rules-change replay case.
	ErrSaveSeedMismatch = "MET-E819"
)
