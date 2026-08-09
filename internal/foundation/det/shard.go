package det

import (
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// NumShards is the fixed shard count the entire determinism model is built
// on (§1.2 point 1). It is a constant forever: never derive a shard count
// from runtime.NumCPU(), a config value, world size, or anything else that
// could vary between machines, builds, or worlds. Every guarantee this
// package (and everything built on it) makes about deterministic merging
// assumes exactly this many shards, always.
const NumShards = 256

// mix64 is the SplitMix64 finalizer (Vigna, 2015): a small, well-studied,
// stdlib-only avalanche mixer. It is used ONLY for shard assignment
// (spreading cell/entity keys uniformly across [0, NumShards)) — it is NOT
// the RNG construction used for simulation draws (see rng.go's
// Philox-style Stream). Pure integer ops only: no wall clock, no map
// iteration, no randomness source.
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// ShardForEntity assigns an id-hash shard for citizens/firms/entities
// (§1.2 point 1, "id-hash for citizens/firms"). It is a pure hash: the
// same id always maps to the same shard, for the lifetime of the program
// and across platforms/builds (mix64 uses only fixed-width integer
// arithmetic). Every uint64 value is a valid id — there is no invalid
// input, so this function never panics and never returns an error
// (AC-8: "impossible by type").
func ShardForEntity(id uint64) int {
	return int(mix64(id) % NumShards)
}

// ShardForCell assigns a spatial shard for a world cell/network node by
// its (x, y) coordinate (§1.2 point 1, "spatial for cells/network"). x and
// y are packed into a single 64-bit key (their low 32 bits each — cell
// coordinates fit comfortably inside int32 range for any world size this
// project targets) and hashed the same way as ShardForEntity. Every
// (int, int) pair is a valid input, including negative coordinates (the
// packing is a bit pattern, not an ordering) — this function never panics
// and never returns an error (AC-8: "impossible by type").
func ShardForCell(x, y int) int {
	packed := uint64(uint32(x))<<32 | uint64(uint32(y))
	return int(mix64(packed) % NumShards)
}

// ShardResult is one worker's output for one shard, as fed into
// MergeInOrder. Shard must be in [0, NumShards).
type ShardResult[T any] struct {
	Shard int
	Value T
}

// MergeInOrder combines exactly NumShards per-shard results, in strict
// ascending shard order (0→255), regardless of the order they appear in
// results (§1.2 point 1: "results are merged in shard order 0→255"). zero
// seeds the accumulator; combine folds each shard's result into it in
// order.
//
// MergeInOrder is deliberately strict rather than permissive (AC-10): it
// errors rather than silently merging an incomplete or duplicated shard
// set, because a silently-incomplete merge would produce a
// plausible-looking but WRONG world state — exactly the class of bug
// determinism testing exists to catch mechanically instead of by review.
func MergeInOrder[T any, A any](correlationID string, results []ShardResult[T], zero A, combine func(acc A, r ShardResult[T]) A) (A, error) {
	if len(results) != NumShards {
		return zero, errs.New(ErrShardMergeIncomplete, correlationID, map[string]any{
			"got":  len(results),
			"want": NumShards,
		})
	}

	sorted := make([]ShardResult[T], len(results))
	copy(sorted, results)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Shard < sorted[j].Shard })

	seen := make([]bool, NumShards)
	for _, r := range sorted {
		if r.Shard < 0 || r.Shard >= NumShards {
			return zero, errs.New(ErrShardOutOfRange, correlationID, map[string]any{"shard": r.Shard})
		}
		if seen[r.Shard] {
			return zero, errs.New(ErrShardDuplicate, correlationID, map[string]any{"shard": r.Shard})
		}
		seen[r.Shard] = true
	}

	acc := zero
	for _, r := range sorted {
		acc = combine(acc, r)
	}
	return acc, nil
}
