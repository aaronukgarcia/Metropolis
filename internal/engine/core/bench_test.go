package core

import "testing"

// BenchmarkAdvanceTicks_SteadyState_ZeroModules is AC-9's steady-state
// allocation benchmark: the walking-skeleton case (zero registered
// PhaseHooks — real module content is out of scope for this package,
// see doc.go) drives one daily tick per iteration after a brief
// warm-up. Run with:
//
//	go test ./internal/engine/core/ -bench BenchmarkAdvanceTicks_SteadyState_ZeroModules -benchmem -run ^$
//
// AC-9 is satisfied via this benchmark path (0 allocs/op), not the
// escape-analysis alternative — see the dispatch report for the
// recorded `-benchmem` output and which alternative was used.
func BenchmarkAdvanceTicks_SteadyState_ZeroModules(b *testing.B) {
	e := NewEngine(WithPoolSize(4))

	// Warm-up: cross a few month boundaries so any one-time setup cost
	// (there is none today, but this keeps the benchmark honest if one
	// is ever added) is paid before the timed loop starts.
	if err := e.AdvanceTicks("bench-warmup", 90); err != nil {
		b.Fatalf("warm-up AdvanceTicks: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := e.AdvanceTicks("bench-corr", 1); err != nil {
			b.Fatalf("AdvanceTicks: %v", err)
		}
	}
}

// BenchmarkSeal_FastPath_NoMutex is BUG-016's proof that seal()'s atomic
// fast path removes the per-AdvanceTicks-call mutex acquisition once an
// Engine is already sealed. It calls seal() directly (bypassing
// AdvanceTicks' n-validation and tick loop, which are irrelevant here)
// after sealing the Engine once, so every timed iteration takes the
// fast path exclusively.
//
// Run with:
//
//	go test ./internal/engine/core/ -bench BenchmarkSeal -benchmem -run ^$
//
// Compare against BenchmarkSeal_SlowPath_MutexOnly below, which measures
// the pre-BUG-016 cost (one uncontended Lock/Unlock per call) by
// resetting sealedFast every iteration so the mutex path is always
// taken. The fast path should show materially lower ns/op with 0
// allocs/op preserved in both cases.
func BenchmarkSeal_FastPath_NoMutex(b *testing.B) {
	e := NewEngine(WithPoolSize(4))
	if err := e.seal("bench-seal-warmup"); err != nil {
		b.Fatalf("warm-up seal: %v", err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := e.seal("bench-seal-fast"); err != nil {
			b.Fatalf("seal: %v", err)
		}
	}
}

// BenchmarkSeal_SlowPath_MutexOnly measures the mutex-acquisition cost
// seal() paid on EVERY call before BUG-016 (and still pays on an
// Engine's first call today): it forces the slow path every iteration
// by clearing sealedFast between calls, restoring the "atomic check
// always indicates work is needed" case the fast path is designed to
// avoid. This is the baseline BenchmarkSeal_FastPath_NoMutex is compared
// against.
func BenchmarkSeal_SlowPath_MutexOnly(b *testing.B) {
	e := NewEngine(WithPoolSize(4))

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		e.sealedFast.Store(false)
		if err := e.seal("bench-seal-slow"); err != nil {
			b.Fatalf("seal: %v", err)
		}
	}
}
