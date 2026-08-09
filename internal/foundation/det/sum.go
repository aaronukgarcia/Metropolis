package det

import "sort"

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
func SumInShardOrder(values []ShardFloat) float64 {
	sorted := make([]ShardFloat, len(values))
	copy(sorted, values)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Shard < sorted[j].Shard })

	var sum float64
	for _, v := range sorted {
		sum += v.Value
	}
	return sum
}
