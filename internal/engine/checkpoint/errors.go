package checkpoint

// Registry error codes for feat.checkpoint. Range: G3500-G3599, claimed
// here per docs/planning/acceptance/README.md's "per-module error
// sub-ranges are claimed at build time" convention. The E layer
// (E000-E999) is fully claimed by eleven earlier engine modules, and the
// G layer's three-digit blocks plus G1000-G3499 were claimed by
// engine.citizens through engine.capexport by the time this package
// landed (engine.comms took G3300-G3399 and engine.capexport G3400-G3499
// in the same wave), so feat.checkpoint opens G3500-G3599 as the next free
// four-digit block under BUG-234's three-to-four-digit code-format
// widening. Checked against BOTH data/errors.json's ranges.reserved table
// AND a live source scan (`grep -rn "MET-G3[5-9]" internal/ cmd/`) before
// claiming, per BUG-008's lesson that the table alone is not always
// current — no prior MET-G35xx code existed either place.
//
// Every code below is registered in data/errors.json with real
// severity/module/message/remedy fields (GR#7); the
// internal/foundation/errs source-scan test
// (TestSourceCodesAreRegisteredAndInRange) guards against this ever
// drifting out of sync.
const (
	// ErrCheckpointCopied: a Manager method was called on a struct copy of
	// the value NewManager returned (SEC-020-class). A copied Manager gets
	// its own independent mu while aliasing the same underlying
	// save.Manager — the two-locks-one-referent shape checkNotCopied
	// exists to reject before mu is ever touched.
	ErrCheckpointCopied = "MET-G3500"

	// ErrInvalidCheckpointID: a checkpoint identifier (the CreateCheckpoint
	// name, a parent, or a revert target) is not a single clean path
	// component — it failed serialize.ValidateShardName. Rejected at the
	// boundary before it is ever joined into a bundle path.
	ErrInvalidCheckpointID = "MET-G3501"

	// ErrCheckpointInProgress: a CreateCheckpoint/Revert trigger arrived
	// while another checkpoint operation was already in flight on the same
	// Manager. Rejected (never queued) so two operations can never
	// interleave a bundle write with the active-head pointer update.
	ErrCheckpointInProgress = "MET-G3502"

	// ErrCheckpointMetaWriteFailed: this package's checkpoint-meta.json
	// lineage sidecar could not be written into a just-saved checkpoint
	// bundle. The just-created bundle is rolled back so no half-recorded
	// checkpoint is left discoverable.
	ErrCheckpointMetaWriteFailed = "MET-G3503"

	// ErrHeadWriteFailed: the active-head pointer file (checkpoint-head.json)
	// could not be written (or promoted) after a checkpoint was created.
	// The just-created bundle is rolled back so the on-disk fork tree never
	// disagrees with the recorded active head.
	ErrHeadWriteFailed = "MET-G3504"

	// ErrHeadReadFailed: the active-head pointer file exists but could not
	// be read or decoded. Surfaced by CurrentID; Lineage degrades to "no
	// active head" rather than failing the whole enumeration.
	ErrHeadReadFailed = "MET-G3505"

	// ErrParentNotFound: CreateCheckpoint was given a non-empty parent ID
	// that does not name an existing checkpoint. Lineage is recorded
	// explicitly (AC-4) and must not dangle.
	ErrParentNotFound = "MET-G3506"

	// ErrNotACheckpoint: Revert was given a target that does not name a
	// checkpoint (its bundle directory exists but carries no
	// checkpoint-meta.json, or is missing/unreadable). Reverting into an
	// arbitrary non-checkpoint save is refused.
	ErrNotACheckpoint = "MET-G3507"

	// ErrLineageFailed: Lineage could not enumerate the manual/ checkpoint
	// subdirectory under a root (a filesystem-level failure — distinct from
	// a single malformed bundle, which is skipped per-entry).
	ErrLineageFailed = "MET-G3508"

	// ErrPruneFailed: pruning's rename phase failed partway. Every branch
	// already staged into the .pruning area is renamed back, so the
	// retained set is left exactly as it was before the prune attempt
	// (AC-9).
	ErrPruneFailed = "MET-G3509"

	// ErrInvalidForkConfig: SetMaxRetainedForks was given a negative value.
	ErrInvalidForkConfig = "MET-G3510"

	// ErrNameOccupied: CreateCheckpoint was given a name that already
	// occupies the manual/ namespace — an existing checkpoint OR a
	// same-named manual save (feat.saveux's SaveManual shares this
	// namespace; SEC-188). Rejected before any save, because re-creating an
	// existing name would silently save-over the prior bundle (destroying
	// player data) and, for a checkpoint, re-parent it (a lineage cycle when
	// the new parent is a descendant of the existing checkpoint). Lineage is
	// fixed at creation (AC-4), so an existing name can never be re-parented.
	ErrNameOccupied = "MET-G3511"

	// ErrForkSeqExhausted: Revert could not derive a free fork name because
	// the persisted monotonic fork counter saturated (int64 max) while
	// skipping past colliding checkpoints (SEC-175). Unreachable in normal
	// play — it indicates a hand-edited head pointer.
	ErrForkSeqExhausted = "MET-G3512"

	// ErrRevertRestoreFailed: a revert failed after it had already loaded
	// the target into the live participants, and reloading the prior active
	// head to undo that also failed (SEC-176). The live state is left
	// reverted while CurrentID is unchanged; surfaced loudly so the caller
	// knows the half-applied state was not fully recovered.
	ErrRevertRestoreFailed = "MET-G3513"

	// ErrCheckpointNameTooLong: CreateCheckpoint was given a name longer
	// than maxCheckpointNameLen. The bound reserves the fork-name suffix
	// budget (forkNamePrefix + maxForkSeqDigits) so every created
	// checkpoint stays revertible at ANY fork sequence — a longer name
	// would create fine but derive a fork name SaveManual rejects
	// (SEC-189), making it permanently unrevertible.
	ErrCheckpointNameTooLong = "MET-G3514"

	// ErrForkNameTooLong: Revert could not fork a checkpoint because the
	// derived fork name ("<target>.fork<seq>") would exceed maxSaveNameLen
	// (save's manual-name limit). A fork checkpoint is itself a valid future
	// revert target, so a fork-of-fork chain grows the name by
	// len(forkNamePrefix)+digits(seq) per level and can reach a fork whose
	// own fork name would overrun the limit (SEC-196). Revert rejects this
	// at derivation, BEFORE any load, so it fails loudly at the revert
	// rather than silently creating a fork that is itself permanently
	// unrevertible — the class SEC-189 fixed at CreateCheckpoint, closed
	// here at the sibling derivation nextFreeForkName.
	ErrForkNameTooLong = "MET-G3515"
)
