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
