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
		ApplyBarrier(shuffled, func(p string) { got = append(got, p) })

		if !reflect.DeepEqual(got, want) {
			t.Fatalf("trial %d: applied order = %v, want %v", trial, got, want)
		}
	}
}

func label(shard, seq int) string {
	return string(rune('a'+shard)) + string(rune('0'+seq))
}
