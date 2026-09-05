package det

// Registry error codes for foundation.det — reserved range F200-F299 in
// data/errors.json's "reserved" table. Every code below IS registered
// there with real severity/module/message/remedy fields (GR#7; closed
// under BUG-008). The internal/foundation/errs source-scan test guards
// against this ever drifting out of sync again.
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

	// ErrBarrierDuplicate: ApplyBarrier, or either single-shard fast path
	// that replicates its sort inline (engine/core's runPhaseForHookFast,
	// foundation/integration's executeSingleShard), was handed two
	// messages/effects with the same (Shard, Sequence) pair. BUG-287
	// closed the gap for ApplyBarrier and runPhaseForHookFast; BUG-370
	// found executeSingleShard had replicated the sort WITHOUT the
	// dedupe (silent last-message-wins on a fast path where the pooled
	// path errored on the exact same input) and closed it by routing all
	// three call sites through the one shared check,
	// det.RejectAdjacentDuplicateKey (barrier.go). Mirrors
	// ErrShardDuplicate's semantic for MergeInOrder: a duplicate
	// canonical key would make the applied order depend on submission
	// order, so it is rejected before any message is applied rather than
	// silently tolerated.
	ErrBarrierDuplicate = "MET-F203"
)
