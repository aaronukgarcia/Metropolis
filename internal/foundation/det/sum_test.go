package det

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// AC-7: summing the same multiset via two different input orderings that
// both map to the same canonical shard order produces bit-identical
// results; feeding shard-tagged values out of canonical order is
// re-sorted before summing rather than trusted as-is.
func TestSumInShardOrder_CanonicalRegardlessOfInputOrder(t *testing.T) {
	canonical := []ShardFloat{
		{Shard: 0, Value: 0.1},
		{Shard: 1, Value: 0.2},
		{Shard: 2, Value: 0.3},
		{Shard: 3, Value: 0.4},
		{Shard: 4, Value: 0.5},
	}

	outOfOrder := []ShardFloat{
		{Shard: 3, Value: 0.4},
		{Shard: 0, Value: 0.1},
		{Shard: 4, Value: 0.5},
		{Shard: 1, Value: 0.2},
		{Shard: 2, Value: 0.3},
	}

	want, err := SumInShardOrder("corr-canon", canonical)
	if err != nil {
		t.Fatalf("SumInShardOrder(canonical) error: %v", err)
	}
	got, err := SumInShardOrder("corr-canon", outOfOrder)
	if err != nil {
		t.Fatalf("SumInShardOrder(outOfOrder) error: %v", err)
	}

	if got != want {
		t.Fatalf("SumInShardOrder(outOfOrder) = %v, want bit-identical %v (canonical order)", got, want)
	}
}

func TestSumInShardOrder_Value(t *testing.T) {
	values := []ShardFloat{
		{Shard: 0, Value: 1.0},
		{Shard: 1, Value: 2.0},
		{Shard: 2, Value: 3.0},
	}
	got, err := SumInShardOrder("corr-value", values)
	if err != nil {
		t.Fatalf("SumInShardOrder error: %v", err)
	}
	if got != 6.0 {
		t.Fatalf("SumInShardOrder = %v, want 6.0", got)
	}
}

// BUG-287 AC-2/AC-6(b): a shard index outside [0, NumShards) is rejected
// with ErrShardOutOfRange.
func TestSumInShardOrder_OutOfRangeShardErrors(t *testing.T) {
	values := []ShardFloat{
		{Shard: 0, Value: 1.0},
		{Shard: NumShards, Value: 2.0},
	}
	sum, err := SumInShardOrder("corr-oor", values)
	if err == nil {
		t.Fatal("SumInShardOrder with out-of-range shard: want error, got nil")
	}
	if !errors.Is(err, &errs.E{Code: ErrShardOutOfRange}) {
		t.Fatalf("SumInShardOrder error code = %v, want ErrShardOutOfRange (%s)", err, ErrShardOutOfRange)
	}
	if sum != 0 {
		t.Fatalf("SumInShardOrder returned sum=%v on error, want 0", sum)
	}
}

// BUG-287 AC-2/AC-6(b): two values sharing the same Shard are rejected
// with ErrShardDuplicate, in BOTH submission orders. Before this fix, the
// unstable sort.Slice's tie-break fell back to submission order, and
// because float64 addition is not associative, an accumulator that
// already holds a much larger value can sum a tied pair differently
// depending on which one lands first (huge+a)+b vs (huge+b)+a — exactly
// the non-reproducible-result class this strictness closes. The
// assertion that matters here is that BOTH orders are now rejected
// outright rather than silently returning either float.
func TestSumInShardOrder_DuplicateShardErrors(t *testing.T) {
	const huge = 1e16
	orderA := []ShardFloat{
		{Shard: 0, Value: huge},
		{Shard: 1, Value: 1.0},
		{Shard: 1, Value: 0.5}, // duplicate Shard 1
	}
	orderB := []ShardFloat{
		{Shard: 0, Value: huge},
		{Shard: 1, Value: 0.5},
		{Shard: 1, Value: 1.0}, // duplicate Shard 1, opposite submission order
	}

	for name, values := range map[string][]ShardFloat{"orderA": orderA, "orderB": orderB} {
		sum, err := SumInShardOrder("corr-dup-"+name, values)
		if err == nil {
			t.Fatalf("%s: SumInShardOrder with duplicate shard: want error, got sum=%v", name, sum)
		}
		if !errors.Is(err, &errs.E{Code: ErrShardDuplicate}) {
			t.Fatalf("%s: SumInShardOrder error code = %v, want ErrShardDuplicate (%s)", name, err, ErrShardDuplicate)
		}
		if sum != 0 {
			t.Fatalf("%s: SumInShardOrder returned sum=%v on error, want 0", name, sum)
		}
	}
}
