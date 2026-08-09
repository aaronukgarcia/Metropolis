package det

import (
	"math"
	"math/rand"
	"testing"
)

// AC-1: NumShards is a fixed constant, 256.
func TestNumShards(t *testing.T) {
	if NumShards != 256 {
		t.Fatalf("NumShards = %d, want 256", NumShards)
	}
}

// AC-2, AC-8: ShardForEntity/ShardForCell always return values in
// [0, NumShards), never panic (including for extreme/negative inputs),
// and ShardForEntity is a pure hash (same id -> same shard, every call).
func TestShardForEntity_RangeAndPurity(t *testing.T) {
	ids := []uint64{0, 1, 2, 42, math.MaxUint64, math.MaxUint64 - 1, 1 << 63}
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 10_000; i++ {
		ids = append(ids, r.Uint64())
	}

	for _, id := range ids {
		s := ShardForEntity(id)
		if s < 0 || s >= NumShards {
			t.Fatalf("ShardForEntity(%d) = %d, out of [0, %d)", id, s, NumShards)
		}
		if again := ShardForEntity(id); again != s {
			t.Fatalf("ShardForEntity(%d) not pure: %d then %d", id, s, again)
		}
	}
}

func TestShardForCell_RangeAndPurity(t *testing.T) {
	type pt struct{ x, y int }
	pts := []pt{
		{0, 0}, {1, 1}, {-1, -1}, {math.MinInt32, math.MaxInt32},
		{math.MaxInt32, math.MinInt32}, {-1, 1}, {1, -1},
	}
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 10_000; i++ {
		pts = append(pts, pt{int(int32(r.Uint32())), int(int32(r.Uint32()))})
	}

	for _, p := range pts {
		s := ShardForCell(p.x, p.y)
		if s < 0 || s >= NumShards {
			t.Fatalf("ShardForCell(%d,%d) = %d, out of [0, %d)", p.x, p.y, s, NumShards)
		}
		if again := ShardForCell(p.x, p.y); again != s {
			t.Fatalf("ShardForCell(%d,%d) not pure: %d then %d", p.x, p.y, s, again)
		}
	}
}

// AC-3: MergeInOrder combines results in strict ascending shard order
// regardless of input completion order, across multiple randomized-order
// trials.
func TestMergeInOrder_OrderIndependent(t *testing.T) {
	base := make([]ShardResult[int], NumShards)
	for i := range base {
		base[i] = ShardResult[int]{Shard: i, Value: i * 7}
	}

	sortedSum, err := MergeInOrder("corr-1", base, 0, func(acc int, r ShardResult[int]) int { return acc + r.Value })
	if err != nil {
		t.Fatalf("MergeInOrder(sorted) error: %v", err)
	}

	sortedConcat, err := MergeInOrder("corr-1", base, "", func(acc string, r ShardResult[int]) string {
		return acc + string(rune('A'+(r.Shard%26)))
	})
	if err != nil {
		t.Fatalf("MergeInOrder(sorted concat) error: %v", err)
	}

	r := rand.New(rand.NewSource(3))
	for trial := 0; trial < 20; trial++ {
		shuffled := make([]ShardResult[int], len(base))
		copy(shuffled, base)
		r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

		sum, err := MergeInOrder("corr-1", shuffled, 0, func(acc int, r ShardResult[int]) int { return acc + r.Value })
		if err != nil {
			t.Fatalf("trial %d: MergeInOrder error: %v", trial, err)
		}
		if sum != sortedSum {
			t.Fatalf("trial %d: sum merge = %d, want %d (order dependence detected)", trial, sum, sortedSum)
		}

		concat, err := MergeInOrder("corr-1", shuffled, "", func(acc string, r ShardResult[int]) string {
			return acc + string(rune('A'+(r.Shard%26)))
		})
		if err != nil {
			t.Fatalf("trial %d: MergeInOrder concat error: %v", trial, err)
		}
		if concat != sortedConcat {
			t.Fatalf("trial %d: concat merge = %q, want %q (order dependence detected)", trial, concat, sortedConcat)
		}
	}
}

// AC-10: MergeInOrder errors (rather than silently merging incompletely)
// on too few shard results, and on a duplicate shard index.
func TestMergeInOrder_IncompleteErrors(t *testing.T) {
	short := make([]ShardResult[int], NumShards-1)
	for i := range short {
		short[i] = ShardResult[int]{Shard: i, Value: i}
	}

	_, err := MergeInOrder("corr-2", short, 0, func(acc int, r ShardResult[int]) int { return acc + r.Value })
	if err == nil {
		t.Fatal("MergeInOrder with NumShards-1 results: want error, got nil")
	}
}

func TestMergeInOrder_DuplicateShardErrors(t *testing.T) {
	dup := make([]ShardResult[int], NumShards)
	for i := range dup {
		dup[i] = ShardResult[int]{Shard: i, Value: i}
	}
	// Duplicate shard 0 by overwriting the entry for shard NumShards-1.
	dup[NumShards-1] = ShardResult[int]{Shard: 0, Value: 999}

	_, err := MergeInOrder("corr-3", dup, 0, func(acc int, r ShardResult[int]) int { return acc + r.Value })
	if err == nil {
		t.Fatal("MergeInOrder with duplicate shard 0: want error, got nil")
	}
}
