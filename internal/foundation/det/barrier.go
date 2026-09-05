package det

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// Message is a cross-shard effect produced during a phase, to be applied
// at the following phase barrier (§1.2 point 2). Shard is the producing
// shard; Sequence is that shard's local emission order for the message
// (e.g. an incrementing counter the shard-local scratch code bumps per
// message it emits during the phase). The pair (Shard, Sequence) is the
// canonical total order messages are applied in — never submission order,
// never a Go map's iteration order.
type Message[T any] struct {
	Shard    int
	Sequence int
	Payload  T
}

// LessCanonicalKey reports whether the (shardA, sequenceA) key sorts
// strictly before (shardB, sequenceB) in the canonical (Shard, Sequence)
// ascending order this package applies cross-shard messages in (§1.2
// point 2): Shard is the primary sort key, Sequence the secondary.
//
// This is the ONE comparator every canonical-order sort in this package
// (and its single-shard fast-path callers) uses — shared rather than
// reimplemented per call site, per GR#3. BUG-370's round found that
// foundation/integration's executeSingleShard had drifted from this by
// sorting on Sequence alone while still comparing the FULL (Shard,
// Sequence) tuple for duplicates: a genuine duplicate key with a
// different-shard message tied at the same Sequence in between (e.g.
// {0,5},{1,5},{0,5}) sorts, under a Sequence-only comparator, into an
// UNSPECIFIED order (sort.Slice is not stable) that can separate the two
// {0,5} entries so they are never adjacent — silently hiding the
// duplicate rather than rejecting it. Sorting by the full tuple via this
// shared comparator makes that impossible: two items sharing a (Shard,
// Sequence) key are always adjacent after the sort, regardless of what
// ties with them on either field alone.
func LessCanonicalKey(shardA, sequenceA, shardB, sequenceB int) bool {
	if shardA != shardB {
		return shardA < shardB
	}
	return sequenceA < sequenceB
}

// ApplyBarrier applies messages in the canonical (Shard, Sequence)
// ascending order, regardless of the order they appear in messages
// (§1.2 point 2). This is what makes cross-shard effects deterministic:
// two producers racing to append to messages in different orders (e.g.
// because they ran on different goroutines, or because of map-iteration
// nondeterminism upstream) still yield the exact same applied order here.
//
// ApplyBarrier is deliberately strict (BUG-287), mirroring MergeInOrder
// (shard.go): every message is validated BEFORE any is applied — a shard
// index outside [0, NumShards) returns ErrShardOutOfRange, and two
// messages sharing the same (Shard, Sequence) pair return
// ErrBarrierDuplicate — because a duplicate key ties under the canonical
// sort and would silently fall back to submission order, which is exactly
// the goroutine-scheduling-dependent nondeterminism this package exists to
// eliminate. On any validation failure, NO message is applied — a partial
// apply on invalid input is itself a plausible-but-wrong result.
func ApplyBarrier[T any](correlationID string, messages []Message[T], apply func(T)) error {
	sorted := make([]Message[T], len(messages))
	copy(sorted, messages)
	sort.Slice(sorted, func(i, j int) bool {
		return LessCanonicalKey(sorted[i].Shard, sorted[i].Sequence, sorted[j].Shard, sorted[j].Sequence)
	})

	for i, m := range sorted {
		if m.Shard < 0 || m.Shard >= NumShards {
			return errs.New(ErrShardOutOfRange, correlationID, map[string]any{"shard": m.Shard})
		}
		if i > 0 {
			if err := RejectAdjacentDuplicateKey(correlationID, m.Shard, m.Sequence, sorted[i-1].Shard, sorted[i-1].Sequence); err != nil {
				return err
			}
		}
	}

	for _, m := range sorted {
		apply(m.Payload)
	}
	return nil
}

// RejectAdjacentDuplicateKey is the ONE implementation of BUG-287's
// duplicate-(Shard,Sequence)-key rejection (GR#3 Single Source of Truth).
// shard/sequence is the item at some index i in a slice already sorted
// into canonical ascending order via LessCanonicalKey; prevShard/
// prevSequence is the item at i-1 in that same slice. If the two keys are
// identical, the pair ties under the canonical sort and would silently
// fall back to submission/goroutine-scheduling order — exactly the
// nondeterminism this package exists to eliminate — so this returns a
// registry-sourced ErrBarrierDuplicate (MET-F203) instead of letting the
// caller apply anything.
//
// Three call sites share this one function, each having already sorted
// its own concrete slice type via LessCanonicalKey — or, for a
// single-shard fast path where every item's Shard is the same constant,
// by Sequence alone, which degenerates to the identical comparison:
//
//   - ApplyBarrier, the general multi-shard barrier.
//   - engine/core's runPhaseForHookFast (phase.go), the SingleShardHook
//     fast path — BUG-287's original single-shard gap.
//   - foundation/integration's executeSingleShard (executor.go), the
//     SingleShard() Integration fast path — BUG-370's matching gap,
//     found because it replicated the canonical sort inline without
//     replicating this check.
//
// BUG-370 (2026-09-05): before this helper existed, the two single-shard
// fast paths either re-implemented the check by hand (core) or omitted
// it entirely (integration) — the same duplicate input errored on the
// pooled/general path but silently applied both messages (last write
// simply happening twice, in canonical order) on the un-checked fast
// path. Routing every caller through this one function makes that class
// of drift impossible: fixing the check here fixes it everywhere.
func RejectAdjacentDuplicateKey(correlationID string, shard, sequence, prevShard, prevSequence int) error {
	if shard == prevShard && sequence == prevSequence {
		return errs.New(ErrBarrierDuplicate, correlationID, map[string]any{
			"shard":    shard,
			"sequence": sequence,
		})
	}
	return nil
}
