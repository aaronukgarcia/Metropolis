package det

import "sort"

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

// ApplyBarrier applies messages in the canonical (Shard, Sequence)
// ascending order, regardless of the order they appear in messages
// (§1.2 point 2). This is what makes cross-shard effects deterministic:
// two producers racing to append to messages in different orders (e.g.
// because they ran on different goroutines, or because of map-iteration
// nondeterminism upstream) still yield the exact same applied order here.
func ApplyBarrier[T any](messages []Message[T], apply func(T)) {
	sorted := make([]Message[T], len(messages))
	copy(sorted, messages)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Shard != sorted[j].Shard {
			return sorted[i].Shard < sorted[j].Shard
		}
		return sorted[i].Sequence < sorted[j].Sequence
	})
	for _, m := range sorted {
		apply(m.Payload)
	}
}
