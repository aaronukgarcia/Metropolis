package det

import "testing"

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

	want := SumInShardOrder(canonical)
	got := SumInShardOrder(outOfOrder)

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
	if got := SumInShardOrder(values); got != 6.0 {
		t.Fatalf("SumInShardOrder = %v, want 6.0", got)
	}
}
