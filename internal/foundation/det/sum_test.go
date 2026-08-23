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

	want, err := SumInShardOrder("corr-sum-canonical", canonical)
	if err != nil {
		t.Fatalf("SumInShardOrder(canonical): unexpected error: %v", err)
	}
	got, err := SumInShardOrder("corr-sum-canonical", outOfOrder)
	if err != nil {
		t.Fatalf("SumInShardOrder(outOfOrder): unexpected error: %v", err)
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
	got, err := SumInShardOrder("corr-sum-value", values)
	if err != nil {
		t.Fatalf("SumInShardOrder: unexpected error: %v", err)
	}
	if got != 6.0 {
		t.Fatalf("SumInShardOrder = %v, want 6.0", got)
	}
}

// BUG-287: two values for the same shard have no intrinsic tiebreak —
// their relative order would be submission order, and float64 addition is
// not associative, so the result would be scheduling-dependent.
// SumInShardOrder rejects duplicates fail-closed (MergeInOrder's
// strictness, AC-10) rather than return a plausible-looking wrong sum.
func TestSumInShardOrder_DuplicateShardRejected(t *testing.T) {
	values := []ShardFloat{
		{Shard: 3, Value: 0.25},
		{Shard: 1, Value: 0.5},
		{Shard: 3, Value: 0.75},
	}
	if _, err := SumInShardOrder("corr-sum-dup", values); err == nil {
		t.Fatal("SumInShardOrder with duplicate shard: want error, got nil")
	}
}
