package integration

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// record is the barrier-applied payload the sample integration below
// emits — captured so tests can compare the FULL sequence of
// ApplyMessage calls (not just the merged accumulator) byte-for-byte
// across dispatch strategies.
type record struct {
	Shard int
	Seq   int
	Value uint64
}

// sumIntegration is the sample Integration these tests exercise: each
// shard draws one seeded det.Stream value (position-independent Philox —
// safe across goroutines, per foundation/det/rng.go), contributes it to
// a running uint64 sum, and emits one ordered barrier message per shard
// recording what it drew. If single is true, RunShard(shard) for any
// shard other than 0 returns the zero contribution and no messages —
// satisfying the SingleShard() promise Execute's fast path depends on
// (executor.go's executeSingleShard doc comment).
//
// applied captures every ApplyMessage call, in the order Execute
// actually invoked them — this is what proves barrier ordering, not just
// the final merged sum, is dispatch-invariant.
type sumIntegration struct {
	seed   uint64
	single bool

	mu      sync.Mutex
	applied []record
}

func newSumIntegration(seed uint64, single bool) *sumIntegration {
	return &sumIntegration{seed: seed, single: single}
}

func (s *sumIntegration) RunShard(shard int) (uint64, []det.Message[uint64]) {
	if s.single && shard != 0 {
		return 0, nil
	}
	stream := det.NewStream(s.seed, uint64(shard), 0, "integration-executor-test")
	v := stream.Uint64() % 1000
	msg := det.Message[uint64]{Shard: shard, Sequence: 0, Payload: v}
	return v, []det.Message[uint64]{msg}
}

func (s *sumIntegration) Combine(acc uint64, r det.ShardResult[uint64]) uint64 {
	return acc + r.Value
}

func (s *sumIntegration) ApplyMessage(m uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// Sequence/Shard are not recoverable from the payload alone in this
	// sample (ApplyMessage's signature is payload-only, matching
	// det.ApplyBarrier's apply func) — record application ORDER via the
	// slice index instead, which is exactly what the (Shard, Sequence)
	// canonical order is supposed to fix regardless of dispatch
	// strategy.
	s.applied = append(s.applied, record{Value: m})
}

func (s *sumIntegration) Zero() uint64 { return 0 }

func (s *sumIntegration) UpdateClass() Class { return ClassT1Batchable }

func (s *sumIntegration) SingleShard() bool { return s.single }

func (s *sumIntegration) Applied() []record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]record, len(s.applied))
	copy(out, s.applied)
	return out
}

// (a) Execute(LocalPool(workers=N)) is byte-identical for N in
// {1,2,4,8,16}: same merged sum, same applied-message sequence.
func TestExecute_LocalPool_WorkerCountInvariant(t *testing.T) {
	workerCounts := []int{1, 2, 4, 8, 16}

	var wantSum uint64
	var wantApplied []record

	for i, workers := range workerCounts {
		in := newSumIntegration(42, false)
		sum, err := Execute[uint64, uint64](fmt.Sprintf("corr-a-%d", workers), NewLocalPool(workers), in)
		if err != nil {
			t.Fatalf("workers=%d: Execute error: %v", workers, err)
		}
		applied := in.Applied()

		if i == 0 {
			wantSum = sum
			wantApplied = applied
			continue
		}
		if sum != wantSum {
			t.Fatalf("workers=%d: sum = %d, want %d (worker-count divergence)", workers, sum, wantSum)
		}
		if !reflect.DeepEqual(applied, wantApplied) {
			t.Fatalf("workers=%d: applied sequence diverged:\n got  %v\n want %v", workers, applied, wantApplied)
		}
	}
}

// (b) Execute(LocalPool) == Execute(SerialPool): dispatch-strategy
// (location) transparency, not just worker-count invariance within one
// strategy.
func TestExecute_LocalVsSerialPool_Identical(t *testing.T) {
	local := newSumIntegration(1337, false)
	localSum, err := Execute[uint64, uint64]("corr-b-local", NewLocalPool(8), local)
	if err != nil {
		t.Fatalf("LocalPool: Execute error: %v", err)
	}

	serial := newSumIntegration(1337, false)
	serialSum, err := Execute[uint64, uint64]("corr-b-serial", NewSerialPool(), serial)
	if err != nil {
		t.Fatalf("SerialPool: Execute error: %v", err)
	}

	if localSum != serialSum {
		t.Fatalf("LocalPool sum = %d, SerialPool sum = %d: dispatch strategy changed the result", localSum, serialSum)
	}
	if !reflect.DeepEqual(local.Applied(), serial.Applied()) {
		t.Fatalf("applied sequence diverged between LocalPool and SerialPool:\n local  %v\n serial %v", local.Applied(), serial.Applied())
	}
}

// (c) Execute(LocalPool) == a direct det.RunPhase call built from the
// same ShardFunc/combine/applyMsg: the executor adds no divergence over
// calling det.RunPhase directly.
func TestExecute_MatchesDirectRunPhase(t *testing.T) {
	seed := uint64(99)

	in := newSumIntegration(seed, false)
	execSum, err := Execute[uint64, uint64]("corr-c-exec", NewLocalPool(4), in)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	var directApplied []record
	var mu sync.Mutex
	shardFn := func(shard int) (uint64, []det.Message[uint64]) {
		stream := det.NewStream(seed, uint64(shard), 0, "integration-executor-test")
		v := stream.Uint64() % 1000
		return v, []det.Message[uint64]{{Shard: shard, Sequence: 0, Payload: v}}
	}
	combine := func(acc uint64, r det.ShardResult[uint64]) uint64 { return acc + r.Value }
	applyMsg := func(m uint64) {
		mu.Lock()
		defer mu.Unlock()
		directApplied = append(directApplied, record{Value: m})
	}

	directSum, err := det.RunPhase[uint64, uint64]("corr-c-direct", 4, 0, shardFn, combine, applyMsg)
	if err != nil {
		t.Fatalf("det.RunPhase error: %v", err)
	}

	if execSum != directSum {
		t.Fatalf("Execute sum = %d, direct det.RunPhase sum = %d", execSum, directSum)
	}
	if !reflect.DeepEqual(in.Applied(), directApplied) {
		t.Fatalf("applied sequence diverged between Execute and direct det.RunPhase:\n exec   %v\n direct %v", in.Applied(), directApplied)
	}
}

// (d) A SingleShard()==true integration produces identical results via
// the fast path (executeSingleShard) and the full 256-shard path — proven
// by running the SAME underlying shard-0-only logic through both, once
// with SingleShard()==true (fast path) and once with SingleShard()==false
// (full path forced by wrapping), and asserting byte-identical output.
type forceFullPathIntegration struct {
	*sumIntegration
}

func (f forceFullPathIntegration) SingleShard() bool { return false }

func TestExecute_SingleShardFastPath_MatchesFullPath(t *testing.T) {
	fast := newSumIntegration(7, true)
	fastSum, err := Execute[uint64, uint64]("corr-d-fast", NewLocalPool(8), fast)
	if err != nil {
		t.Fatalf("fast path: Execute error: %v", err)
	}

	slowUnderlying := newSumIntegration(7, true) // RunShard still zero-contributes for shard!=0
	slow := forceFullPathIntegration{slowUnderlying}
	slowSum, err := Execute[uint64, uint64]("corr-d-slow", NewLocalPool(8), slow)
	if err != nil {
		t.Fatalf("full path: Execute error: %v", err)
	}

	if fastSum != slowSum {
		t.Fatalf("fast-path sum = %d, full-path sum = %d", fastSum, slowSum)
	}
	if !reflect.DeepEqual(fast.Applied(), slowUnderlying.Applied()) {
		t.Fatalf("applied sequence diverged between fast and full path:\n fast %v\n full %v", fast.Applied(), slowUnderlying.Applied())
	}
}

// TestExecute_SingleShardFastPath_SkipsOtherShards proves the fast path
// really does skip shards 1..255 (not just happen to produce the same
// result): an integration whose RunShard panics for any shard != 0 must
// still succeed under the fast path.
type panicOnOtherShardsIntegration struct{}

func (panicOnOtherShardsIntegration) RunShard(shard int) (uint64, []det.Message[uint64]) {
	if shard != 0 {
		panic("fast path dispatched a non-zero shard")
	}
	return 5, nil
}
func (panicOnOtherShardsIntegration) Combine(acc uint64, r det.ShardResult[uint64]) uint64 {
	return acc + r.Value
}
func (panicOnOtherShardsIntegration) ApplyMessage(uint64) {}
func (panicOnOtherShardsIntegration) Zero() uint64        { return 0 }
func (panicOnOtherShardsIntegration) UpdateClass() Class  { return ClassT0Critical }
func (panicOnOtherShardsIntegration) SingleShard() bool   { return true }

func TestExecute_SingleShardFastPath_NeverCallsOtherShards(t *testing.T) {
	sum, err := Execute[uint64, uint64]("corr-d-nopanics", NewLocalPool(4), panicOnOtherShardsIntegration{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if sum != 5 {
		t.Fatalf("sum = %d, want 5", sum)
	}
}

// --- (e) BUG-304: dev-mode assert against a lying SingleShard() --------

// lyingSingleShardIntegration claims SingleShard()==true but breaks the
// promise: shard 7 (chosen arbitrarily, != 0) contributes a non-zero value
// AND emits a message, exactly the "real work happens on a shard other
// than 0" case executeSingleShard's doc comment warns silently loses work.
type lyingSingleShardIntegration struct{}

func (lyingSingleShardIntegration) RunShard(shard int) (uint64, []det.Message[uint64]) {
	if shard == 7 {
		return 999, []det.Message[uint64]{{Shard: 7, Sequence: 0, Payload: 999}}
	}
	if shard == 0 {
		return 5, nil
	}
	return 0, nil
}
func (lyingSingleShardIntegration) Combine(acc uint64, r det.ShardResult[uint64]) uint64 {
	return acc + r.Value
}
func (lyingSingleShardIntegration) ApplyMessage(uint64) {}
func (lyingSingleShardIntegration) Zero() uint64        { return 0 }
func (lyingSingleShardIntegration) UpdateClass() Class  { return ClassT0Critical }
func (lyingSingleShardIntegration) SingleShard() bool   { return true }

// TestExecute_SingleShardFastPath_SilentlyDropsWithoutAssert is the RED
// half's control: WITHOUT WithSingleShardAssert, BUG-304's exact failure
// mode reproduces — shard 7's contribution is silently dropped, no panic,
// no error, just a wrong (too-small) result. This documents the pre-fix
// (and still-default, opt-in-only) behaviour; it is not itself the
// regression test for the fix (that is
// TestExecute_SingleShardAssert_CatchesLyingIntegration below), since a
// lying SingleShard() *silently losing work* is exactly what
// executeSingleShard's doc comment says the fast path does by design when
// the assert is off.
func TestExecute_SingleShardFastPath_SilentlyDropsWithoutAssert(t *testing.T) {
	sum, err := Execute[uint64, uint64]("corr-e-silent", NewLocalPool(4), lyingSingleShardIntegration{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if sum != 5 {
		t.Fatalf("sum = %d, want 5 (shard 7's contribution silently dropped, not 1004)", sum)
	}
}

// TestExecute_SingleShardAssert_CatchesLyingIntegration is BUG-304's
// actual regression test (Bro audit, 2026-08-20): passing
// WithSingleShardAssert(true) must catch the SAME lying integration above
// by panicking, mirroring engine/core's WithSingleShardAssert/BUG-269
// precedent (phase_test.go) for this package's Execute/executeSingleShard.
func TestExecute_SingleShardAssert_CatchesLyingIntegration(t *testing.T) {
	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				msg, ok := r.(string)
				if !ok || !containsBUG304Marker(msg) {
					t.Fatalf("panic value = %v, want a string containing the BUG-304 SingleShard() promise-broken marker", r)
				}
			}
		}()
		_, _ = Execute[uint64, uint64]("corr-e-assert", NewLocalPool(4), lyingSingleShardIntegration{}, WithSingleShardAssert(true))
	}()
	if !panicked {
		t.Fatalf("Execute with WithSingleShardAssert(true) against a lying SingleShard() integration: did not panic, want a panic (BUG-304)")
	}
}

func containsBUG304Marker(s string) bool {
	return strings.Contains(s, "SingleShard() promise broken")
}

// TestExecute_SingleShardAssert_PassesForAnHonestIntegration proves the
// assert has no false positives: an integration that genuinely honours
// SingleShard() must still succeed (and produce the same result as
// without the assert) when WithSingleShardAssert(true) is passed — the
// assert must never fire on a promise that actually holds.
func TestExecute_SingleShardAssert_PassesForAnHonestIntegration(t *testing.T) {
	in := newSumIntegration(11, true)
	sum, err := Execute[uint64, uint64]("corr-e-honest", NewLocalPool(4), in, WithSingleShardAssert(true))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	unassertedIn := newSumIntegration(11, true)
	wantSum, err := Execute[uint64, uint64]("corr-e-honest-control", NewLocalPool(4), unassertedIn)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if sum != wantSum {
		t.Fatalf("assert-enabled sum = %d, unasserted sum = %d, want equal (assert must never change an honest integration's result)", sum, wantSum)
	}
}

// --- (f) BUG-304 round 2: the assert's self-comparison hole for a ----------
// --- reference-typed T with an in-place-mutating Combine ------------------

// ptrAccumulator is a reference-typed accumulator -- the idiomatic
// map/pointer fold this probe must be safe against: Combine below mutates
// its acc argument IN PLACE and returns that SAME pointer, exactly the
// shape det.MergeInOrder's contract permits and the first version of
// WithSingleShardAssert's probe (in.Combine(merged, ...) compared back
// against merged) could not distinguish from a fresh value -- since
// `combined` and `merged` would alias the identical *ptrAccumulator,
// reflect.DeepEqual(combined, merged) was trivially true no matter what a
// lying shard contributed.
type ptrAccumulator struct {
	Sum uint64
}

// lyingPointerIntegration claims SingleShard()==true but shard 7 lies,
// exactly like lyingSingleShardIntegration above, restated over a
// reference-typed T to reproduce BUG-304's round-2 finding: the
// independent round's probe showed the OLD assert let this integration's
// lie through completely undetected (no panic) AND corrupted the real
// returned value in the process (assert-off correctly returned Sum=5,
// assert-ON silently returned Sum=1004 -- the merged accumulator polluted
// by 255 probe folds it should never have been exposed to).
//
// Deliberately a VALUE-ONLY lie -- shard 7 contributes a non-zero Sum but
// emits NO message. A message-emitting lie (like
// lyingSingleShardIntegration's shard 7) would trip the probe's
// len(extraMsgs) != 0 check regardless of whether the VALUE comparison
// itself was sound, masking the self-comparison hole this fixture exists
// to isolate: the old probe's failure was specifically in the value-only
// path (in.Combine(merged, ...) compared back against merged), so this
// must lie ONLY through the value channel to prove that path is fixed.
type lyingPointerIntegration struct{}

func (lyingPointerIntegration) RunShard(shard int) (*ptrAccumulator, []det.Message[uint64]) {
	switch shard {
	case 7:
		return &ptrAccumulator{Sum: 999}, nil
	case 0:
		return &ptrAccumulator{Sum: 5}, nil
	default:
		return &ptrAccumulator{Sum: 0}, nil
	}
}

// Combine is the idiomatic in-place-mutating reference-type fold: it
// mutates acc directly and returns the SAME pointer, never allocating a
// new *ptrAccumulator for the result.
func (lyingPointerIntegration) Combine(acc *ptrAccumulator, r det.ShardResult[*ptrAccumulator]) *ptrAccumulator {
	acc.Sum += r.Value.Sum
	return acc
}
func (lyingPointerIntegration) ApplyMessage(uint64)   {}
func (lyingPointerIntegration) Zero() *ptrAccumulator { return &ptrAccumulator{} }
func (lyingPointerIntegration) UpdateClass() Class    { return ClassT0Critical }
func (lyingPointerIntegration) SingleShard() bool     { return true }

// honestPointerIntegration is lyingPointerIntegration's honest twin: every
// shard other than 0 genuinely contributes the zero value and no
// messages, so WithSingleShardAssert(true) must pass it cleanly and must
// never corrupt its correct result.
type honestPointerIntegration struct{}

func (honestPointerIntegration) RunShard(shard int) (*ptrAccumulator, []det.Message[uint64]) {
	if shard == 0 {
		return &ptrAccumulator{Sum: 5}, nil
	}
	return &ptrAccumulator{Sum: 0}, nil
}
func (honestPointerIntegration) Combine(acc *ptrAccumulator, r det.ShardResult[*ptrAccumulator]) *ptrAccumulator {
	acc.Sum += r.Value.Sum
	return acc
}
func (honestPointerIntegration) ApplyMessage(uint64)   {}
func (honestPointerIntegration) Zero() *ptrAccumulator { return &ptrAccumulator{} }
func (honestPointerIntegration) UpdateClass() Class    { return ClassT0Critical }
func (honestPointerIntegration) SingleShard() bool     { return true }

// TestExecute_SingleShardAssert_ReferenceTypedT_CatchesLyingIntegration is
// BUG-304 round 2's regression test (independent destructive round,
// 2026-08-20): the assert-off run must still return the correct (if
// incomplete) Sum=5 -- proving the fast path itself is fine and isolating
// the assert's own correctness -- and the assert-ON run against the SAME
// lying integration must PANIC rather than silently pass through (the
// self-comparison hole) or return a corrupted Sum=1004 (the
// merged-accumulator-pollution hole).
func TestExecute_SingleShardAssert_ReferenceTypedT_CatchesLyingIntegration(t *testing.T) {
	off, err := Execute[*ptrAccumulator, uint64]("corr-f-ptr-off", NewLocalPool(4), lyingPointerIntegration{})
	if err != nil {
		t.Fatalf("Execute (assert off): %v", err)
	}
	if off.Sum != 5 {
		t.Fatalf("assert-off Sum = %d, want 5 (fast path itself must still be correct)", off.Sum)
	}

	panicked := false
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				msg, ok := r.(string)
				if !ok || !containsBUG304Marker(msg) {
					t.Fatalf("panic value = %v, want a string containing the BUG-304 SingleShard() promise-broken marker", r)
				}
			}
		}()
		_, _ = Execute[*ptrAccumulator, uint64]("corr-f-ptr-on", NewLocalPool(4), lyingPointerIntegration{}, WithSingleShardAssert(true))
	}()
	if !panicked {
		t.Fatalf("Execute with WithSingleShardAssert(true) against a reference-typed lying integration: did not panic, want a panic " +
			"(BUG-304 round 2: the self-comparison hole let this through undetected and returned a corrupted Sum=1004)")
	}
}

// TestExecute_SingleShardAssert_ReferenceTypedT_HonestIntegrationUncorrupted
// proves the fix has no false positives AND no side effects on an honest
// reference-typed integration's result: probing shard 1..255 against a
// FRESH in.Zero() (never `merged`) must leave the real merged accumulator
// completely untouched.
func TestExecute_SingleShardAssert_ReferenceTypedT_HonestIntegrationUncorrupted(t *testing.T) {
	assertOn, err := Execute[*ptrAccumulator, uint64]("corr-f-ptr-honest-on", NewLocalPool(4), honestPointerIntegration{}, WithSingleShardAssert(true))
	if err != nil {
		t.Fatalf("Execute (assert on, honest integration): %v", err)
	}
	if assertOn.Sum != 5 {
		t.Fatalf("assert-ON Sum = %d, want 5 (the probe must never corrupt an honest integration's result)", assertOn.Sum)
	}
}

// --- (g) BUG-304 round 3: a lying Zero() (shared-singleton) is caught -----
// --- at the source, as its OWN contract violation --------------------------

// singletonZero is the shared, package-level accumulator
// singletonZeroIntegration's Zero() illegally returns on every call — the
// exact contract violation integration.go's Zero() doc now forbids
// explicitly. It is never mutated by this test: the R3 aliasing check
// (zeroAliased, executor.go) panics BEFORE either probed Zero() result is
// ever passed to Combine, so this singleton's Sum field is read but never
// written across the whole test run.
var singletonZero = &ptrAccumulator{}

// singletonZeroIntegration reproduces BUG-304 round 3 (Bro audit,
// independent destructive round, 2026-08-20): a reference-typed
// Integration whose Zero() returns ONE SHARED object every call instead
// of a fresh one. Before R3, executeSingleShard's probe called in.Zero()
// twice per shard (freshA, freshB) and trusted they were independent —
// exactly the assumption this Zero() breaks. freshA and freshB alias the
// SAME *ptrAccumulator, so the probe's own comparison degenerates into
// the identical self-comparison class round 1's `merged`-based probe
// suffered from, reborn one layer deeper through a Zero() shape
// integration.go's interface never mechanically forbade before this
// round.
type singletonZeroIntegration struct{}

func (singletonZeroIntegration) RunShard(shard int) (*ptrAccumulator, []det.Message[uint64]) {
	switch shard {
	case 7:
		return &ptrAccumulator{Sum: 999}, nil
	case 0:
		return &ptrAccumulator{Sum: 5}, nil
	default:
		return &ptrAccumulator{Sum: 0}, nil
	}
}
func (singletonZeroIntegration) Combine(acc *ptrAccumulator, r det.ShardResult[*ptrAccumulator]) *ptrAccumulator {
	acc.Sum += r.Value.Sum
	return acc
}
func (singletonZeroIntegration) ApplyMessage(uint64)   {}
func (singletonZeroIntegration) Zero() *ptrAccumulator { return singletonZero } // CONTRACT VIOLATION: same object every call
func (singletonZeroIntegration) UpdateClass() Class    { return ClassT0Critical }
func (singletonZeroIntegration) SingleShard() bool     { return true }

// TestExecute_SingleShardAssert_LyingZero_PanicsNamingZeroContract is
// BUG-304 round 3's regression test (promoted from the independent
// round's attack, inverted per the lead's R3 ruling): a singleton-Zero()
// integration must now PANIC, and the panic must specifically name the
// Zero() contract violation — distinct from a generic
// "SingleShard() promise broken" message — so a future maintainer
// debugging this sees exactly which of the two Execute-time contracts
// (SingleShard() vs Zero()) was actually broken.
func TestExecute_SingleShardAssert_LyingZero_PanicsNamingZeroContract(t *testing.T) {
	panicked := false
	var panicMsg string
	func() {
		defer func() {
			if r := recover(); r != nil {
				panicked = true
				if s, ok := r.(string); ok {
					panicMsg = s
				}
			}
		}()
		_, _ = Execute[*ptrAccumulator, uint64]("corr-g-singleton-zero", NewLocalPool(4), singletonZeroIntegration{}, WithSingleShardAssert(true))
	}()
	if !panicked {
		t.Fatalf("Execute with WithSingleShardAssert(true) against a singleton-Zero() integration: did not panic, want a panic " +
			"(BUG-304 round 3: a lying Zero() must be caught as its own contract violation)")
	}
	if !strings.Contains(panicMsg, "Zero() contract violation") {
		t.Fatalf("panic message = %q, want it to explicitly name the Zero() contract violation (not a generic SingleShard() failure)", panicMsg)
	}
}

// ignoresAccIntegration is the honest, non-mutating alternative Combine
// shape: it discards its acc argument entirely and returns r.Value
// as-is, rather than the idiomatic in-place mutate-and-return-acc fold
// every other reference-typed fixture in this file uses. This exists to
// prove the R3 aliasing check produces NO false positive against a
// perfectly ordinary Combine that never touches acc at all — Zero() here
// is honest (a genuinely fresh *ptrAccumulator every call), so the
// zeroAliased check correctly finds no aliasing and the assert must stay
// green.
type ignoresAccIntegration struct{}

func (ignoresAccIntegration) RunShard(shard int) (*ptrAccumulator, []det.Message[uint64]) {
	if shard == 0 {
		return &ptrAccumulator{Sum: 5}, nil
	}
	return &ptrAccumulator{Sum: 0}, nil
}
func (ignoresAccIntegration) Combine(_ *ptrAccumulator, r det.ShardResult[*ptrAccumulator]) *ptrAccumulator {
	return r.Value
}
func (ignoresAccIntegration) ApplyMessage(uint64)   {}
func (ignoresAccIntegration) Zero() *ptrAccumulator { return &ptrAccumulator{} }
func (ignoresAccIntegration) UpdateClass() Class    { return ClassT0Critical }
func (ignoresAccIntegration) SingleShard() bool     { return true }

// TestExecute_SingleShardAssert_IgnoresAccCombine_StaysGreen is the
// counter-case the R3 ruling requires kept green: an honest,
// non-mutating Combine plus an honest (fresh-every-call) Zero() must
// pass WithSingleShardAssert(true) cleanly, with the correct result.
func TestExecute_SingleShardAssert_IgnoresAccCombine_StaysGreen(t *testing.T) {
	sum, err := Execute[*ptrAccumulator, uint64]("corr-g-ignores-acc", NewLocalPool(4), ignoresAccIntegration{}, WithSingleShardAssert(true))
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if sum.Sum != 5 {
		t.Fatalf("Sum = %d, want 5", sum.Sum)
	}
}
