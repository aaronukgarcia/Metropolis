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
		if sorted[i].Shard != sorted[j].Shard {
			return sorted[i].Shard < sorted[j].Shard
		}
		return sorted[i].Sequence < sorted[j].Sequence
	})

	for i, m := range sorted {
		if m.Shard < 0 || m.Shard >= NumShards {
			return errs.New(ErrShardOutOfRange, correlationID, map[string]any{"shard": m.Shard})
		}
		if i > 0 && sorted[i-1].Shard == m.Shard && sorted[i-1].Sequence == m.Sequence {
			return errs.New(ErrBarrierDuplicate, correlationID, map[string]any{
				"shard":    m.Shard,
				"sequence": m.Sequence,
			})
		}
	}

	for _, m := range sorted {
		apply(m.Payload)
	}
	return nil
}
