package integration

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ATTACK 1: five messages, duplicate LAST in canonical order. If any
// effect at position < k were applied incrementally, applied would be
// non-empty.
func TestAttackBUG370_DupLast_NothingApplied(t *testing.T) {
	msgs := []det.Message[string]{
		{Shard: 0, Sequence: 0, Payload: "a"},
		{Shard: 0, Sequence: 1, Payload: "b"},
		{Shard: 0, Sequence: 2, Payload: "c"},
		{Shard: 0, Sequence: 9, Payload: "dup-1"},
		{Shard: 0, Sequence: 9, Payload: "dup-2"},
	}
	fast := &multiMsgIntegration{single: true, shard0Msgs: msgs}
	_, err := Execute[uint64, string]("attack-dup-last-fast", NewLocalPool(4), fast)
	if err == nil {
		t.Fatal("fast: want error")
	}
	if !errors.Is(err, &errs.E{Code: det.ErrBarrierDuplicate}) {
		t.Fatalf("fast: err=%v want ErrBarrierDuplicate", err)
	}
	if got := fast.Applied(); len(got) != 0 {
		t.Fatalf("fast: PARTIAL APPLY, applied=%v", got)
	}

	slowU := &multiMsgIntegration{single: true, shard0Msgs: msgs}
	_, err2 := Execute[uint64, string]("attack-dup-last-pooled", NewLocalPool(4), forceFullPathMultiMsgIntegration{slowU})
	if err2 == nil {
		t.Fatal("pooled: want error")
	}
	if got := slowU.Applied(); len(got) != 0 {
		t.Fatalf("pooled: PARTIAL APPLY, applied=%v", got)
	}
}

// ATTACK 3: the fast path sorts by Sequence ONLY but compares (Shard,
// Sequence). A SingleShard integration whose shard-0 messages carry
// mixed Shard fields can therefore hide a genuine duplicate key from the
// adjacent-pair check, while the pooled path (which sorts by the full
// (Shard, Sequence) tuple) still rejects it.
func TestAttackBUG370_MixedShardTieHidesDuplicate(t *testing.T) {
	msgs := []det.Message[string]{
		{Shard: 0, Sequence: 5, Payload: "s0-a"},
		{Shard: 1, Sequence: 5, Payload: "s1"},
		{Shard: 0, Sequence: 5, Payload: "s0-b"},
	}
	fast := &multiMsgIntegration{single: true, shard0Msgs: msgs}
	_, fastErr := Execute[uint64, string]("attack-mixed-fast", NewLocalPool(4), fast)

	slowU := &multiMsgIntegration{single: true, shard0Msgs: msgs}
	_, slowErr := Execute[uint64, string]("attack-mixed-pooled", NewLocalPool(4), forceFullPathMultiMsgIntegration{slowU})

	t.Logf("fastErr=%v applied=%v", fastErr, fast.Applied())
	t.Logf("slowErr=%v applied=%v", slowErr, slowU.Applied())

	if (fastErr == nil) != (slowErr == nil) {
		t.Fatalf("DIVERGENCE: fastErr=%v slowErr=%v", fastErr, slowErr)
	}
}

// ATTACK 4: duplicate (Shard, Sequence) with IDENTICAL payloads must be
// refused too - identical payloads are still two applies.
func TestAttackBUG370_IdenticalPayloadDuplicateRefused(t *testing.T) {
	msgs := []det.Message[string]{
		{Shard: 0, Sequence: 7, Payload: "same"},
		{Shard: 0, Sequence: 7, Payload: "same"},
	}
	fast := &multiMsgIntegration{single: true, shard0Msgs: msgs}
	_, err := Execute[uint64, string]("attack-ident-fast", NewLocalPool(4), fast)
	if err == nil {
		t.Fatalf("fast: identical-payload duplicate NOT refused; applied=%v", fast.Applied())
	}
	if got := fast.Applied(); len(got) != 0 {
		t.Fatalf("fast: applied=%v", got)
	}
}

// ATTACK: two messages that differ only by Shard at the same Sequence
// (a legitimate pair for ApplyBarrier) must NOT be refused - proves the
// helper's equality is on the tuple, not on Sequence alone.
func TestAttackBUG370_DifferentShardSameSequenceAllowed(t *testing.T) {
	if err := det.RejectAdjacentDuplicateKey("attack", 1, 5, 0, 5); err != nil {
		t.Fatalf("different shards, same sequence must be allowed: %v", err)
	}
	if err := det.RejectAdjacentDuplicateKey("attack", 0, 6, 0, 5); err != nil {
		t.Fatalf("same shard, different sequence must be allowed: %v", err)
	}
	if err := det.RejectAdjacentDuplicateKey("attack", 0, 5, 0, 5); err == nil {
		t.Fatal("identical key must be refused")
	}
	// negative values are still keys
	if err := det.RejectAdjacentDuplicateKey("attack", -3, -9, -3, -9); err == nil {
		t.Fatal("identical negative key must be refused")
	}
}
