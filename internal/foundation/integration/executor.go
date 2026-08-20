package integration

import (
	"fmt"
	"reflect"
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
func Execute[T any, M any](correlationID string, pool WorkerPool, in Integration[T, M], opts ...ExecuteOption) (T, error) {
	var cfg executeConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	if in.SingleShard() {
		return executeSingleShard[T, M](correlationID, in, cfg.assertSingleShard)
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

// executeConfig holds Execute's opt-in dev-mode settings (currently just
// assertSingleShard). Never grown into a general public struct — options
// are the extension point, mirroring engine/core's Option/Engine-options
// pattern (engine.go) exactly, for the same reason: adding a field here
// later is source-compatible with every existing Execute call, adding a
// positional parameter would not be.
type executeConfig struct {
	assertSingleShard bool
}

// ExecuteOption customizes a single Execute call. Mirrors engine/core's
// Option type (engine.go) for this package's Execute/executeSingleShard
// pair.
type ExecuteOption func(*executeConfig)

// WithSingleShardAssert enables a DEV-MODE-ONLY safety net for
// executeSingleShard's SingleShard() fast path — BUG-304 (Bro audit,
// 2026-08-20), mirroring engine/core's WithSingleShardAssert
// (internal/engine/core/engine.go) and runPhaseForHookFast
// (internal/engine/core/phase.go) IN SPIRIT, restated for this package's
// Integration[T, M]/RunShard/Combine shape instead of
// PhaseHook/RunShard/Effect — NOT identical in mechanism, because
// RunShard's signature (T, []det.Message[M]), unlike PhaseHook.RunShard,
// carries no error return to check: when true, executeSingleShard
// additionally calls RunShard for every shard in [1, det.NumShards) —
// the same 255 calls the full (pooled) Execute path would have made —
// and panics if any of them return any messages, or contribute anything
// other than a true no-op when folded (via a FRESH in.Zero(), never the
// real accumulator — see executeSingleShard's doc comment for why, and
// for exactly what is and is not guaranteed for a reference-typed T,
// which depends on Integration.Zero()'s contract holding — a violation
// of THAT contract is itself mechanically detected and panics separately,
// naming Zero() as the culprit), proving the integration's SingleShard()
// promise actually holds.
//
// This intentionally pays the full per-shard cost the fast path exists to
// avoid, so it must NEVER be enabled in production: defaults to false
// (Execute's zero-value executeConfig), and is meant for tests (or an
// explicit local debug run) that want the extra assurance that an
// Integration opting into SingleShard() is telling the truth, not a
// per-call production safeguard. Before this, a lying SingleShard()
// silently lost work with no way to catch it short of an
// executor_test.go-style equivalence test against the full path — this
// closes that gap the way BUG-269's engine/core precedent already did for
// PhaseHook.
func WithSingleShardAssert(enabled bool) ExecuteOption {
	return func(c *executeConfig) { c.assertSingleShard = enabled }
}

// zeroAliased reports whether a and b — two values returned by separate
// calls to an Integration's Zero() method — are actually the SAME
// underlying object, i.e. whether Zero() has violated its contract
// (integration.go: "every call returns a fresh, non-aliased value").
//
// Object identity is only a meaningful, checkable question for the
// reflect Kinds that carry a pointer-like identity distinct from their
// content: Ptr, Map, Slice, and Chan (UnsafePointer included for
// completeness, though no Integration in this codebase uses it as T).
// reflect.Value.Pointer() is defined for exactly these kinds and returns
// the address/data-pointer identifying WHICH underlying object a value
// refers to, as opposed to reflect.DeepEqual's content comparison. Every
// other Kind (numbers, strings, structs-of-value-fields, etc.) is a pure
// value type: a copy can never alias another copy's storage in the way
// that would defeat executeSingleShard's probe, so this reports false
// (not aliased, and correctly so) for all of them without inspecting
// them further.
//
// A nil pointer/map/slice/chan on either side is deliberately treated as
// NOT aliased (returns false) even though Pointer() would report 0 for
// both: two independently-nil values share no live backing storage to
// corrupt, so there is nothing here for the Zero() contract to have
// actually violated, and treating nil==nil as a violation would be a
// false positive against a perfectly ordinary (if unusual) Zero()
// implementation.
func zeroAliased(a, b any) bool {
	va := reflect.ValueOf(a)
	vb := reflect.ValueOf(b)
	if !va.IsValid() || !vb.IsValid() || va.Kind() != vb.Kind() {
		return false
	}
	switch va.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Chan, reflect.UnsafePointer:
		if va.IsNil() || vb.IsNil() {
			return false
		}
		return va.Pointer() == vb.Pointer()
	default:
		return false
	}
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
// engine/core. BUG-304 (Bro audit, 2026-08-20) closed the gap this doc
// comment used to flag ("no dev-mode assertion equivalent to
// WithSingleShardAssert in this increment"): passing
// WithSingleShardAssert(true) to Execute now pays for the other 255
// shards anyway and panics if the promise doesn't hold — see that
// option's doc comment for the full argument, which mirrors engine/core's
// precedent exactly. Callers that want assurance without that extra cost
// on every call can still use the full path (SingleShard() == false), or
// verify the promise via executor_test.go-style equivalence tests against
// it, as this increment's own tests do.
func executeSingleShard[T any, M any](correlationID string, in Integration[T, M], assertSingleShard bool) (T, error) {
	value0, msgs0 := in.RunShard(0)

	merged := in.Combine(in.Zero(), det.ShardResult[T]{Shard: 0, Value: value0})

	if assertSingleShard {
		// The SingleShard() contract (this function's doc comment, point
		// 1): in.Combine(acc, ShardResult{Shard: s, Value: in.RunShard(s)})
		// == acc for every s != 0. Checked directly here rather than
		// assumed -- reflect.DeepEqual is safe on this dev-mode-only,
		// opt-in, non-hot, non-committed-state path (never reached unless
		// a caller explicitly passes WithSingleShardAssert(true)), exactly
		// mirroring why engine/core's equivalent assert is safe to run
		// off the production hot path.
		//
		// BUG-304 round 1 (Bro audit, 2026-08-20): the FIRST version of
		// this probe folded each extra shard's contribution onto `merged`
		// itself (in.Combine(merged, ...)) and compared the result back
		// against `merged`. For a reference-typed T whose idiomatic
		// Combine mutates its acc argument in place and returns that SAME
		// reference (a map/pointer fold — det.MergeInOrder's own contract
		// permits exactly this, see phase.go/det doc comments), that
		// probe was a SELF-comparison: `combined` and `merged` alias the
		// same object, so reflect.DeepEqual was trivially true no matter
		// what the lying shard contributed, AND every probe call
		// permanently corrupted the real `merged` this function is about
		// to return.
		//
		// BUG-304 round 2 (independent destructive round, same date):
		// fixed round 1 by folding each extra shard onto a FRESH
		// in.Zero() call instead of `merged`, and comparing that against
		// a SECOND, separately obtained in.Zero() call. That closes round
		// 1's specific hole, but does NOT by itself guarantee the two
		// Zero() calls are actually independent — integration.go's Zero()
		// contract (as it stood) required nothing about cross-call
		// freshness. Round 2's own attacker built exactly that: a
		// reference-typed Integration whose Zero() returns one shared
		// package-level singleton on every call. The two "fresh" in.Zero()
		// calls below then alias each other identically to how `merged`
		// aliased itself in round 1 — same class of bug, reborn one layer
		// deeper, because a foreseeable (and previously unforbidden)
		// Zero() shape defeated the fix that had just closed the previous
		// shape.
		//
		// R3 (lead ruling, 2026-08-20) closes the CLASS rather than this
		// one more instance: integration.go's Zero() doc now makes
		// "every call returns a fresh, non-aliased value" an explicit,
		// mechanically-checked contract requirement (see that doc
		// comment). zeroAliased below verifies the two Zero() calls are
		// NOT the same underlying object BEFORE either is used for
		// comparison — for the reflect Kinds where object identity is
		// even meaningful (Ptr/Map/Slice/Chan/UnsafePointer; every other
		// Kind is a value type, which cannot alias in the way that would
		// defeat this probe). If Zero() itself is lying, that is now
		// caught and reported AS a Zero() contract violation, distinct
		// from (and diagnosed separately from) a SingleShard() promise
		// violation — a lying Zero() is a defect in the integration, not
		// something this executor can silently route around.
		//
		// What this DOES and does NOT guarantee (correcting round 2's
		// overclaim that the fix "can never self-compare regardless of
		// whether T is a value or reference type"): for a VALUE-typed T,
		// self-comparison is structurally impossible (there is no shared
		// identity to alias) and always safe. For a REFERENCE-typed T,
		// safety now depends on the Zero() contract holding -- guaranteed
		// HERE only because zeroAliased mechanically enforces it (a
		// violation panics loudly, naming Zero() as the culprit, rather
		// than silently degrading into round 1/round 2's failure modes
		// again).
		for shard := 1; shard < det.NumShards; shard++ {
			extraValue, extraMsgs := in.RunShard(shard)
			freshA := in.Zero()
			freshB := in.Zero()
			if zeroAliased(freshA, freshB) {
				panic(fmt.Sprintf(
					"BUG-304 Zero() contract violation: two separate calls to Integration.Zero() returned the SAME underlying "+
						"object (shard=%d, correlationID=%s) — integration.go requires every Zero() call to return a fresh, "+
						"non-aliased value; a shared/reused accumulator defeats WithSingleShardAssert's probe and silently "+
						"corrupts Execute's real merged result. Fix the Integration's Zero() method.",
					shard, correlationID,
				))
			}
			base := in.Combine(freshA, det.ShardResult[T]{Shard: shard, Value: extraValue})
			if len(extraMsgs) != 0 || !reflect.DeepEqual(base, freshB) {
				panic(fmt.Sprintf(
					"BUG-304 SingleShard() promise broken: shard %d's contribution was not a no-op against a fresh zero accumulator and/or returned %d messages (correlationID=%s)",
					shard, len(extraMsgs), correlationID,
				))
			}
		}
	}

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
