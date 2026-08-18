package integration

import (
	"sort"
	"sync"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// Execute runs in through pool, following the same two-stage
// deterministic pipeline det.RunPhase itself uses (foundation/det/
// phase.go): every shard's RunShard is dispatched via pool, the
// per-shard results are merged in strict ascending shard order via
// det.MergeInOrder, and every emitted cross-shard message is applied in
// canonical (shard, sequence) order via det.ApplyBarrier. Execute adds no
// ordering decision of its own — see doc.go's "What this package
// guarantees" section for the full argument — so its result is
// byte-identical regardless of which WorkerPool ran it or how many
// workers that pool used (executor_test.go proves this mechanically).
//
// correlationID is passed straight through to det.MergeInOrder for its
// registry-sourced error context (GR#7) — never used for any ordering
// decision.
func Execute[T any, M any](correlationID string, pool WorkerPool, in Integration[T, M]) (T, error) {
	if in.SingleShard() {
		return executeSingleShard[T, M](in)
	}

	numShards := det.NumShards
	results := make([]det.ShardResult[T], numShards)

	var msgMu sync.Mutex
	var messages []det.Message[M]

	pool.Dispatch(numShards, func(shard int) {
		value, msgs := in.RunShard(shard)
		results[shard] = det.ShardResult[T]{Shard: shard, Value: value}
		if len(msgs) > 0 {
			msgMu.Lock()
			messages = append(messages, msgs...)
			msgMu.Unlock()
		}
	})

	merged, err := det.MergeInOrder[T, T](correlationID, results, in.Zero(), in.Combine)
	if err != nil {
		// A merge-level failure (e.g. det.ErrShardMergeIncomplete) is
		// already a registry-sourced *errs.E from the det package —
		// propagate unchanged (mirrors engine/core's runPhaseForHook).
		return in.Zero(), err
	}

	det.ApplyBarrier(messages, in.ApplyMessage)

	return merged, nil
}

// executeSingleShard is the BUG-269-style fast path for an Integration
// that has opted into SingleShard() == true: it calls RunShard exactly
// once, for shard 0, inline — no WorkerPool dispatch, no 256-shard
// fan-out — then applies the resulting messages directly, sorted by
// Sequence.
//
// # Why this is byte-identical to the full path
//
// This mirrors engine/core's runPhaseForHookFast (internal/engine/core/
// phase.go) argument exactly, restated for this package's Combine/Zero
// shape instead of PhaseHook/Effect:
//
//  1. The full path's det.MergeInOrder call folds all 256 per-shard
//     results via in.Combine, seeded at in.Zero(). A SingleShard
//     integration's contract (like SingleShardHook's) is that RunShard
//     for every shard other than 0 returns a value whose Combine
//     contribution is a no-op against the accumulator it is folded into
//     — i.e. in.Combine(acc, ShardResult{Shard: s, Value: in.RunShard(s)})
//     == acc for every s != 0. Given that, folding in.Zero() through
//     shards 0..255 in order collapses to exactly
//     in.Combine(in.Zero(), ShardResult{Shard: 0, Value: v0}), which is
//     what this fast path computes directly — skipping the no-op folds
//     changes nothing observable.
//  2. The full path's det.ApplyBarrier applies every message in
//     canonical (Shard, Sequence) order. A SingleShard integration's
//     promise is that shard 0 is the only shard that ever emits a
//     message, so every message's Shard component is the same constant
//     value and the (Shard, Sequence) sort degenerates to a
//     Sequence-only sort of RunShard(0)'s own returned messages — exactly
//     what this fast path does by sorting msgs0 by Sequence and applying
//     them in that order.
//
// If an integration's SingleShard() promise is false — real work happens
// on a shard other than 0, or a shard other than 0 emits a message — this
// silently drops that work, exactly as runPhaseForHookFast documents for
// engine/core. There is no dev-mode assertion equivalent to
// WithSingleShardAssert in this increment; callers that need one should
// use the full path (SingleShard() == false) until a later increment adds
// it, or verify the promise via executor_test.go-style equivalence tests
// against the full path, as this increment's own tests do.
func executeSingleShard[T any, M any](in Integration[T, M]) (T, error) {
	value0, msgs0 := in.RunShard(0)

	merged := in.Combine(in.Zero(), det.ShardResult[T]{Shard: 0, Value: value0})

	if len(msgs0) > 1 {
		sorted := make([]det.Message[M], len(msgs0))
		copy(sorted, msgs0)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].Sequence < sorted[j].Sequence })
		msgs0 = sorted
	}
	for _, m := range msgs0 {
		in.ApplyMessage(m.Payload)
	}

	return merged, nil
}
