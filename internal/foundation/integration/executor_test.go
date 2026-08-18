package integration

import (
	"fmt"
	"reflect"
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
