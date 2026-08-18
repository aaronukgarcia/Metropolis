package integration

import "sync"

// WorkerPool abstracts HOW a set of shard indices get dispatched to work
// — the seam doc.go's "location transparency" section describes. Dispatch
// must call work(shard) for every shard in [0, numShards) exactly once,
// and must not return until every call has completed (including any
// state work has written back before returning) — that is the pool's
// entire contract. Everything about WHERE and in what order those calls
// physically happen (goroutines stealing from a queue, a strict serial
// loop, or — a later increment — shipping the shard to a remote worker
// and blocking for its response) is invisible to Execute, which only
// consumes the per-shard results work leaves behind and always merges
// them via det.MergeInOrder/det.ApplyBarrier regardless of dispatch
// strategy. That invariance is exactly what makes a future RemotePool a
// drop-in WorkerPool with no change to Execute, the Integration contract,
// or any existing caller (proposal §7).
//
// work must be safe to call concurrently from multiple goroutines when
// Workers() > 1 — the same obligation det.ShardFunc places on a hook's
// RunShard.
type WorkerPool interface {
	// Workers reports this pool's configured worker/concurrency count.
	// Advisory only (e.g. for logging/monitoring) — Dispatch's behaviour,
	// not this value, is what determines dispatch order.
	Workers() int

	// Dispatch calls work(shard) for every shard in [0, numShards),
	// blocking until all calls have returned.
	Dispatch(numShards int, work func(shard int))
}

// LocalPool dispatches shard work across Workers goroutines that steal
// shard indices from a shared queue — the direct local analogue of
// det.RunPhase's own internal dispatch loop (foundation/det/phase.go).
// This is today's only real execution strategy; SerialPool below exists
// purely to prove Execute's result does not depend on which strategy ran
// it.
type LocalPool struct {
	// workers is the goroutine count Dispatch spreads shard work across.
	// workers < 1 is treated as 1, matching det.RunPhase's own
	// "workers < 1 is treated as 1" rule (a single-goroutine run is
	// always a valid degenerate case, never an error).
	workers int
}

// NewLocalPool constructs a LocalPool with the given worker count.
// workers < 1 is treated as 1 — see the LocalPool.workers doc comment.
func NewLocalPool(workers int) LocalPool {
	if workers < 1 {
		workers = 1
	}
	return LocalPool{workers: workers}
}

// Workers reports the configured goroutine count.
func (p LocalPool) Workers() int { return p.workers }

// Dispatch spreads [0, numShards) across p.Workers() goroutines that
// steal shard indices from a shared channel, exactly as
// det.RunPhase does. Blocks until every shard has been processed.
func (p LocalPool) Dispatch(numShards int, work func(shard int)) {
	workers := p.workers
	if workers < 1 {
		workers = 1
	}
	if workers > numShards {
		workers = numShards
	}
	if numShards <= 0 {
		return
	}

	shardQueue := make(chan int, numShards)
	for s := 0; s < numShards; s++ {
		shardQueue <- s
	}
	close(shardQueue)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for shard := range shardQueue {
				work(shard)
			}
		}()
	}
	wg.Wait()
}

// SerialPool dispatches shard work on a single goroutine, strictly in
// shard order 0→numShards-1, one shard at a time. This is the degenerate
// shape a naive, one-shard-at-a-time remote worker would produce — it
// exists to prove (executor_test.go) that Execute's result is invariant
// to dispatch strategy, not merely to worker count within one strategy.
type SerialPool struct{}

// NewSerialPool constructs a SerialPool. No configuration: by
// definition it always runs exactly one worker, strictly in order.
func NewSerialPool() SerialPool { return SerialPool{} }

// Workers always reports 1 — SerialPool never runs more than one shard
// at a time.
func (SerialPool) Workers() int { return 1 }

// Dispatch calls work(shard) for shard = 0, 1, ..., numShards-1, in that
// exact order, on the calling goroutine. No concurrency at all — the
// simplest possible conforming WorkerPool.
func (SerialPool) Dispatch(numShards int, work func(shard int)) {
	for shard := 0; shard < numShards; shard++ {
		work(shard)
	}
}
