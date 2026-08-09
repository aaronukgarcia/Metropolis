package det

// Placeholder registry error codes for foundation.det.
//
// data/errors.json reserves MET-F200-F299 for this module (see its
// "reserved" table), but none of the codes below are registered there
// yet — registry wiring (adding severity/module/message/remedy entries)
// is noted as future work, per the MOD-004 brief. Passing an
// unregistered code to errs.New/errs.Wrap is not a silent failure: the
// errs package (GR#7) detects the unregistered code at construction time
// and transparently falls back to the always-available MET-F003
// "unregistered error code" wrapper, so every error path below already
// fails loudly today and will simply pick up its real registry entry
// (message/remedy/severity) the moment someone lands it in
// data/errors.json — no call site here will need to change.
const (
	// ErrShardOutOfRange: a shard index fell outside [0, NumShards) when
	// validating a caller-supplied ShardResult/Message.
	ErrShardOutOfRange = "MET-F200"

	// ErrShardMergeIncomplete: MergeInOrder was handed a different number
	// of shard results than NumShards expects (AC-10).
	ErrShardMergeIncomplete = "MET-F201"

	// ErrShardDuplicate: MergeInOrder was handed two results for the same
	// shard index (AC-10).
	ErrShardDuplicate = "MET-F202"

	// ErrMoneyOverflow: a Micropounds arithmetic helper detected an
	// int64 overflow rather than silently wrapping/truncating (AC-11).
	ErrMoneyOverflow = "MET-F220"
)
