package det

import (
	"math/rand"
	"reflect"
	"testing"
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
		if err := ApplyBarrier("corr-barrier-canonical", shuffled, func(p string) { got = append(got, p) }); err != nil {
			t.Fatalf("trial %d: unexpected error: %v", trial, err)
		}

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: applied order = %v, want %v", trial, got, want)
		}
	}
}

// BUG-287: two messages with the same (Shard, Sequence) have no intrinsic
// tiebreak — sort.Slice would leave their relative order to submission
// order, which is goroutine-scheduling-dependent. ApplyBarrier rejects
// them fail-closed BEFORE applying anything (MergeInOrder's strictness,
// AC-10), never a silently order-dependent application.
func TestApplyBarrier_DuplicateKeyRejected(t *testing.T) {
	msgs := []Message[string]{
		{Shard: 2, Sequence: 1, Payload: "x"},
		{Shard: 1, Sequence: 0, Payload: "a"},
		{Shard: 2, Sequence: 1, Payload: "y"},
	}

	var applied []string
	err := ApplyBarrier("corr-barrier-dup", msgs, func(p string) { applied = append(applied, p) })
	if err == nil {
		t.Fatal("ApplyBarrier with duplicate (shard, sequence): want error, got nil")
	}
	if len(applied) != 0 {
		t.Fatalf("ApplyBarrier applied %v before failing; want no partial application", applied)
	}
}

func label(shard, seq int) string {
	return string(rune('a'+shard)) + string(rune('0'+seq))
}
