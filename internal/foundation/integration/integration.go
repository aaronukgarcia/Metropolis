package integration

import "github.com/aaronukgarcia/Metropolis/internal/foundation/det"

// Class names an integration's update class (proposal §3): how urgently
// and how often its work must run. The ICD (a later increment, proposal
// §8 point 4) will declare this per integration; this increment only
// defines the closed enum so the contract shape is settled before the
// queue layer (which dispatches differently per class) is built on top of
// it.
type Class int

const (
	// ClassT0Critical is every-tick, must-not-drop work (population,
	// money, conservation) — proposal §3.
	ClassT0Critical Class = iota
	// ClassT1Batchable is heavy work processed in cadence-driven batches,
	// sharded, cloud-offloadable (e.g. large demographic sweeps, traffic
	// assignment) — proposal §3.
	ClassT1Batchable
	// ClassT2Coalescible is latest-wins telemetry/UI state, safe to drop
	// intermediate frames — proposal §3.
	ClassT2Coalescible
)

// String renders a Class for logs/diagnostics. Never used on a
// determinism-sensitive path (no merge or ordering decision reads this).
func (c Class) String() string {
	switch c {
	case ClassT0Critical:
		return "T0-critical"
	case ClassT1Batchable:
		return "T1-batchable"
	case ClassT2Coalescible:
		return "T2-coalescible"
	default:
		return "unknown"
	}
}

// Integration is the contract a module implements to plug into the
// location-transparent executor (proposal §2, §7). It is deliberately
// shaped to match det.RunPhase's own parameters one-for-one — RunShard is
// a ShardFunc, Combine is RunPhase's combine func, and ApplyMessage is
// RunPhase's applyMsg func — so Execute (executor.go) can drive an
// Integration through exactly the same deterministic merge/barrier
// primitives det.RunPhase itself uses, without adding any ordering logic
// of its own.
//
// Every method here must be pure and shard-local except ApplyMessage,
// which is the barrier-time exception (see det/barrier.go): it may touch
// integration-owned accumulator state, but is called single-goroutine, in
// canonical (shard, sequence) order, only after every RunShard call for
// this Execute has returned — the same contract engine/core's
// PhaseHook.ApplyEffect already has.
type Integration[T any, M any] interface {
	// RunShard computes this integration's shard-local work for shard,
	// returning a merge-ready partial result of type T plus zero or more
	// cross-shard messages of payload type M to be applied at the
	// barrier. Must not read or write any state shared with another
	// shard — the whole determinism contract Execute's worker-count/
	// worker-pool invariance depends on (mirrors det.ShardFunc's
	// contract exactly).
	RunShard(shard int) (T, []det.Message[M])

	// Combine folds one shard's ShardResult into the running accumulator,
	// in the strict ascending shard order det.MergeInOrder enforces.
	// Signature matches det.RunPhase's own combine parameter so Execute
	// can hand it straight to det.MergeInOrder.
	Combine(acc T, r det.ShardResult[T]) T

	// ApplyMessage applies one barrier message's payload. Called from
	// det.ApplyBarrier, strictly in canonical (shard, sequence) order,
	// after every shard's RunShard has returned.
	ApplyMessage(m M)

	// Zero returns the accumulator's zero/identity value — the seed
	// det.MergeInOrder folds every shard's Combine into.
	Zero() T

	// UpdateClass reports this integration's T0/T1/T2 class (proposal
	// §3). Not yet consumed by Execute in this increment (the queue
	// layer that dispatches by class is a later increment) — recorded
	// here so the contract is complete and callers can start declaring
	// it now.
	UpdateClass() Class

	// SingleShard reports whether this integration's RunShard only ever
	// produces real work (a non-zero contribution to T, or any messages)
	// for shard 0 — mirrors engine/core's SingleShardHook (BUG-269).
	// Must be a compile-time-constant promise: Execute decides once,
	// per call, which path to take, with no per-shard fallback. An
	// integration that returns true here but does real work on a shard
	// other than 0 silently loses that work — see executor.go's
	// executeSingleShard doc comment.
	SingleShard() bool
}
