package det

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// toyPhase is the "toy per-shard computation exercising cross-shard
// messages" AC-12 asks for: each shard's per-shard value is a function of
// its own index and a couple of RNG draws from an entity keyed by that
// shard (exercising rng.go too), and each shard also emits a message to
// its neighbour shard ((shard+1) mod NumShards) carrying a payload that
// depends on the shard and a local sequence counter — so both the merge
// stage (AC-3/AC-12) and the barrier stage (AC-4/AC-12) are exercised by
// a single RunPhase call.
func toyPhase(correlationID string, workers int) (int64, []string) {
	shardFn := func(shard int) (int64, []Message[string]) {
		st := NewStream(12345, uint64(shard), 7, "toy-phase")
		draw := int64(st.Uint64() % 1000)
		value := int64(shard)*int64(shard) + draw

		neighbour := (shard + 1) % NumShards
		msgs := []Message[string]{
			{Shard: neighbour, Sequence: 0, Payload: fmt.Sprintf("from-%d-seq0", shard)},
			{Shard: neighbour, Sequence: 1, Payload: fmt.Sprintf("from-%d-seq1", shard)},
		}
		return value, msgs
	}

	var applied []string
	var mu sync.Mutex
	combine := func(acc int64, r ShardResult[int64]) int64 { return acc + r.Value }
	applyMsg := func(p string) {
		mu.Lock()
		applied = append(applied, p)
		mu.Unlock()
	}

	merged, err := RunPhase(correlationID, workers, int64(0), shardFn, combine, applyMsg)
	if err != nil {
		panic(err) // test helper; caller asserts via testing.T
	}
	return merged, applied
}

// AC-12: same inputs at simulated worker counts 1/2/4/14 => bit-identical
// merged results and identical applied-message order (the primitive-level
// counterpart to feat.detgate's full-engine hash comparison).
func TestRunPhase_WorkerCountInvariance(t *testing.T) {
	workerCounts := []int{1, 2, 4, 14}

	var wantSum int64
	var wantApplied []string

	for i, workers := range workerCounts {
		sum, applied := toyPhase("corr-phase", workers)
		if i == 0 {
			wantSum = sum
			wantApplied = applied
			continue
		}
		if sum != wantSum {
			t.Fatalf("workers=%d: merged sum = %d, want %d (worker-count dependence detected)", workers, sum, wantSum)
		}
		if !reflect.DeepEqual(applied, wantApplied) {
			t.Fatalf("workers=%d: applied messages = %v, want %v", workers, applied, wantApplied)
		}
	}
}

// AC-13: -race must be clean when multiple goroutines draw from
// different RNG streams concurrently, and when multiple goroutines merge
// different shard ranges concurrently. (This test is meaningful only
// under `go test -race`; run without -race it merely asserts
// correctness.)
func TestConcurrentStreamsAndMerges_RaceClean(t *testing.T) {
	const goroutines = 16

	// Concurrent draws from different streams: each goroutine owns its
	// own Stream value (no shared RNG object) and only ever touches it.
	var wg sync.WaitGroup
	results := make([][]uint64, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			st := NewStream(999, uint64(g), 3, "race-test")
			out := make([]uint64, 100)
			for i := range out {
				out[i] = st.Uint64()
			}
			results[g] = out
		}(g)
	}
	wg.Wait()

	// Each goroutine's own sequential draws must match At() computed
	// independently after the fact (position independence holds even
	// though the draws happened concurrently with other streams).
	for g := 0; g < goroutines; g++ {
		st := NewStream(999, uint64(g), 3, "race-test")
		for i, v := range results[g] {
			if want := st.At(uint64(i)); v != want {
				t.Fatalf("goroutine %d draw %d = %d, want %d", g, i, v, want)
			}
		}
	}

	// Concurrent merges of disjoint shard ranges: each goroutine builds
	// and merges its own full 256-result set independently (MergeInOrder
	// itself takes no shared mutable state), verifying no data race
	// across simultaneous MergeInOrder calls.
	var wg2 sync.WaitGroup
	sums := make([]int64, goroutines)
	for g := 0; g < goroutines; g++ {
		wg2.Add(1)
		go func(g int) {
			defer wg2.Done()
			results := make([]ShardResult[int64], NumShards)
			for s := 0; s < NumShards; s++ {
				results[s] = ShardResult[int64]{Shard: s, Value: int64(s + g)}
			}
			sum, err := MergeInOrder(fmt.Sprintf("corr-%d", g), results, int64(0), func(acc int64, r ShardResult[int64]) int64 { return acc + r.Value })
			if err != nil {
				t.Errorf("goroutine %d: MergeInOrder error: %v", g, err)
				return
			}
			sums[g] = sum
		}(g)
	}
	wg2.Wait()

	for g, sum := range sums {
		want := int64(0)
		for s := 0; s < NumShards; s++ {
			want += int64(s + g)
		}
		if sum != want {
			t.Fatalf("goroutine %d: merged sum = %d, want %d", g, sum, want)
		}
	}
}
