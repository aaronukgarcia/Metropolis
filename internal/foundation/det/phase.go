package det

import "sync"

// ShardFunc computes one shard's phase-local work: shard-local scratch
// producing a merge-ready result of type T, plus zero or more cross-shard
// effect messages of payload type M to be applied at the barrier (§1.2
// point 2: "shards write only shard-local scratch; cross-shard effects
// are emitted as messages"). ShardFunc must not read or write any state
// shared with other shards — that is the whole determinism contract this
// package exists to enforce; RunPhase's worker-count invariance guarantee
// only holds if ShardFunc honours it.
type ShardFunc[T any, M any] func(shard int) (T, []Message[M])

// RunPhase runs shardFn once for every shard in [0, NumShards), spread
// across `workers` goroutines that steal shard indices from a shared work
// queue (§1.2 point 1: "workers steal shards"), then:
//
//  1. merges the NumShards per-shard results in strict shard order 0→255
//     via MergeInOrder (§1.2 point 1);
//  2. applies every cross-shard message emitted by any shard, in
//     canonical (shard, sequence) order, via ApplyBarrier (§1.2 point 2).
//
// The merged result and the sequence of applyMsg calls are byte-identical
// regardless of `workers` — 1, 2, 4, 14, or any other positive count —
// because shard assignment (which shard computes what) is fixed, and both
// merge and barrier stages re-sort into canonical order before combining,
// discarding whatever order goroutine scheduling happened to produce this
// run. This is the primitive engine.core's POOL-SIM builds its tick
// pipeline on: call RunPhase once per simulation phase.
//
// workers < 1 is treated as 1 (a single-goroutine run is always a valid,
// and useful for testing, degenerate case — never an error).
func RunPhase[T any, M any](correlationID string, workers int, zero T, shardFn ShardFunc[T, M], combine func(acc T, r ShardResult[T]) T, applyMsg func(M)) (T, error) {
	if workers < 1 {
		workers = 1
	}

	shardQueue := make(chan int, NumShards)
	for s := 0; s < NumShards; s++ {
		shardQueue <- s
	}
	close(shardQueue)

	resultsCh := make(chan ShardResult[T], NumShards)

	var msgMu sync.Mutex
	var messages []Message[M]

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for shard := range shardQueue {
				value, msgs := shardFn(shard)
				resultsCh <- ShardResult[T]{Shard: shard, Value: value}
				if len(msgs) > 0 {
					msgMu.Lock()
					messages = append(messages, msgs...)
					msgMu.Unlock()
				}
			}
		}()
	}

	wg.Wait()
	close(resultsCh)

	results := make([]ShardResult[T], 0, NumShards)
	for r := range resultsCh {
		results = append(results, r)
	}

	merged, err := MergeInOrder(correlationID, results, zero, combine)
	if err != nil {
		return zero, err
	}

	if err := ApplyBarrier(correlationID, messages, applyMsg); err != nil {
		return zero, err
	}

	return merged, nil
}
