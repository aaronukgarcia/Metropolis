package det

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// AC-4: ApplyBarrier applies messages in canonical (shard, sequence)
// order, not submission order, even when submitted out of both shard
// order and per-shard sequence order.
func TestApplyBarrier_CanonicalOrder(t *testing.T) {
	var msgs []Message[string]
	for shard := 0; shard < 8; shard++ {
		for seq := 0; seq < 4; seq++ {
			msgs = append(msgs, Message[string]{Shard: shard, Sequence: seq, Payload: label(shard, seq)})
		}
	}

	var want []string
	for shard := 0; shard < 8; shard++ {
		for seq := 0; seq < 4; seq++ {
			want = append(want, label(shard, seq))
		}
	}

	r := rand.New(rand.NewSource(4))
	for trial := 0; trial < 20; trial++ {
		shuffled := make([]Message[string], len(msgs))
		copy(shuffled, msgs)
		r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

		var got []string
		if err := ApplyBarrier("corr-canonical", shuffled, func(p string) { got = append(got, p) }); err != nil {
			t.Fatalf("trial %d: ApplyBarrier error: %v", trial, err)
		}

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: applied order = %v, want %v", trial, got, want)
		}
	}
}

// BUG-287 AC-1/AC-6(b): a shard index outside [0, NumShards) is rejected
// with ErrShardOutOfRange, and nothing is applied.
func TestApplyBarrier_OutOfRangeShardErrors(t *testing.T) {
	msgs := []Message[string]{
		{Shard: 0, Sequence: 0, Payload: "a"},
		{Shard: NumShards, Sequence: 0, Payload: "bad"},
	}

	applied := 0
	err := ApplyBarrier("corr-oor", msgs, func(string) { applied++ })
	if err == nil {
		t.Fatal("ApplyBarrier with out-of-range shard: want error, got nil")
	}
	if !errors.Is(err, &errs.E{Code: ErrShardOutOfRange}) {
		t.Fatalf("ApplyBarrier error code = %v, want ErrShardOutOfRange (%s)", err, ErrShardOutOfRange)
	}
	if applied != 0 {
		t.Fatalf("ApplyBarrier applied %d messages despite an out-of-range shard, want 0", applied)
	}
}

// BUG-287 AC-1/AC-6(a): two messages sharing the same (Shard, Sequence)
// pair are rejected with ErrBarrierDuplicate and NOTHING is applied —
// checked in BOTH submission orders, since the bug this closes is that
// the old unstable sort.Slice tolerated the tie and applied whichever
// message happened to sort first, silently depending on submission
// order. A test that only tried one order could not distinguish "always
// applies the first one" (still order-dependent, still wrong) from
// "correctly rejects" — trying both, and asserting apply is never
// called, is what actually proves strictness rather than luck.
func TestApplyBarrier_DuplicateShardSequenceErrors(t *testing.T) {
	orderA := []Message[string]{
		{Shard: 2, Sequence: 5, Payload: "first"},
		{Shard: 2, Sequence: 5, Payload: "second"},
		{Shard: 0, Sequence: 0, Payload: "unrelated"},
	}
	orderB := []Message[string]{
		{Shard: 2, Sequence: 5, Payload: "second"},
		{Shard: 0, Sequence: 0, Payload: "unrelated"},
		{Shard: 2, Sequence: 5, Payload: "first"},
	}

	for name, msgs := range map[string][]Message[string]{"orderA": orderA, "orderB": orderB} {
		var applied []string
		err := ApplyBarrier("corr-dup-"+name, msgs, func(p string) { applied = append(applied, p) })
		if err == nil {
			t.Fatalf("%s: ApplyBarrier with duplicate (Shard,Sequence): want error, got nil (applied=%v)", name, applied)
		}
		if !errors.Is(err, &errs.E{Code: ErrBarrierDuplicate}) {
			t.Fatalf("%s: ApplyBarrier error code = %v, want ErrBarrierDuplicate (%s)", name, err, ErrBarrierDuplicate)
		}
		if len(applied) != 0 {
			t.Fatalf("%s: ApplyBarrier applied %v despite a duplicate (Shard,Sequence) pair, want nothing applied", name, applied)
		}
	}
}

func label(shard, seq int) string {
	return string(rune('a'+shard)) + string(rune('0'+seq))
}
