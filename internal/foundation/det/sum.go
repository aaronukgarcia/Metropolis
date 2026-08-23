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
// SumInShardOrder is deliberately strict rather than permissive (BUG-287,
// mirroring MergeInOrder's AC-10 stance): two values for the same shard
// have no intrinsic tiebreak — an unstable sort would order them by
// submission order, and non-associative float64 addition would then make
// the SUM itself scheduling-dependent. It returns a registry-sourced
// ErrShardDuplicate instead of a plausible-looking wrong total.
func SumInShardOrder(correlationID string, values []ShardFloat) (float64, error) {
	sorted := make([]ShardFloat, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Shard < sorted[j].Shard })
	for i := 1; i < len(sorted); i++ {
		if sorted[i].Shard == sorted[i-1].Shard {
			return 0, errs.New(ErrShardDuplicate, correlationID, map[string]any{
				"shard": sorted[i].Shard,
			})
		}
	}

	var sum float64
	for _, v := range sorted {
		sum += v.Value
	}
	return sum, nil
}
