package det

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ShardFloat tags a float64 value with the shard it originated from, for
// SumInShardOrder.
type ShardFloat struct {
	Shard int
	Value float64
}

// SumInShardOrder sums values in strict ascending Shard order, regardless
// of the order they appear in the values slice (§1.2 point 4: "where
// float64 is unavoidable ... summation is performed in fixed shard
// order"). It re-sorts by Shard before summing rather than trusting the
// caller's input order, so a caller that (accidentally, e.g. via
// unsynchronized goroutine collection or a prior map-iteration bug
// upstream) hands values in a non-canonical order still gets the same,
// reproducible result float64 addition is not associative, so this is
// the one place float64 aggregation is allowed at all: everywhere else,
// use Micropounds or another fixed-point/int64 accumulator.
//
// SumInShardOrder is deliberately strict (BUG-287), mirroring
// MergeInOrder (shard.go): every value is validated BEFORE any is summed
// — a shard index outside [0, NumShards) returns ErrShardOutOfRange, and
// two values sharing the same Shard return ErrShardDuplicate — because a
// duplicate Shard ties under the sort and would silently fall back to
// submission order, and float64 addition is not associative, so a tied
// order produces a different, non-reproducible sum. On any validation
// failure, the returned float64 is 0 and must be ignored (mirroring
// MergeInOrder's zero-value-on-error contract). There is no production
// caller of this function today (grep-verified, BUG-287) — this makes it
// strict ahead of one, at zero risk.
func SumInShardOrder(correlationID string, values []ShardFloat) (float64, error) {
	sorted := make([]ShardFloat, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Shard < sorted[j].Shard })

	for i, v := range sorted {
		if v.Shard < 0 || v.Shard >= NumShards {
			return 0, errs.New(ErrShardOutOfRange, correlationID, map[string]any{"shard": v.Shard})
		}
		if i > 0 && sorted[i-1].Shard == v.Shard {
			return 0, errs.New(ErrShardDuplicate, correlationID, map[string]any{"shard": v.Shard})
		}
	}

	var sum float64
	for _, v := range sorted {
		sum += v.Value
	}
	return sum, nil
}
