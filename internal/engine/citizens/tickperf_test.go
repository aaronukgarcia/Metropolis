package citizens

import (
	"fmt"
	"runtime"
	"runtime/debug"
	"testing"
	"time"
)

// buildPairedRecords builds n cold records as N/2 mutual partner pairs
// (id=2k-1 <-> id=2k, sharing a household), all ~25 years old (inside the
// fertility age band) so applyFertilityLocked's partner lookup
// (fertility.go: `partnerRec, ok := c.coldRecord(partner)`) actually fires
// for every acting (lower-id) partner every scheduled tick — this is
// BUG-666's target path. An earlier version of this harness reused
// mkRecord's own Household/Partner formula (id/2, id/2+1) directly, which
// is fine for the byte-size tests it was written for but NEVER satisfies
// applyFertilityLocked's `partner >= id` acting-partner gate for id>2 (the
// formula always resolves partner < id), so that harness measured
// applyMonthly's baseline linear hazard-draw cost only and never actually
// exercised the O(shard size) rowOf scan this bug fixes — confirmed by a
// throwaway pprof CPU profile during development: hash/fnv-backed
// det.NewStream draws dominated and fertility did not appear in the top 25
// frames at 3M. Partner ids are assigned
// across the FULL id range (not sequential neighbours only), so a partner
// lookup is very likely a cross-shard, real map/scan lookup, exactly the
// case rowOf's doc comment describes.
func buildPairedRecords(n int, out []ColdRecord) []ColdRecord {
	pairs := n / 2
	for k := 0; k < pairs; k++ {
		idA := uint64(2*k + 1)
		idB := uint64(2*k + 2)
		household := uint64(k + 1)
		a := mkRecord(idA, uint16(k%64))
		a.BirthMonth = -300 // ~25 years old at month 0, inside the fertility age band
		a.Household = household
		a.Partner = idB
		a.ChildCount = 0
		b := mkRecord(idB, uint16(k%64))
		b.BirthMonth = -300
		b.Household = household
		b.Partner = idA
		b.ChildCount = 0
		out = append(out, a, b)
	}
	if n%2 == 1 {
		r := mkRecord(uint64(n), uint16(n%64))
		r.BirthMonth = -300
		r.Partner = 0
		out = append(out, r)
	}
	return out
}

// seedPaired seeds n paired citizens (buildPairedRecords) into a fresh
// CitizensAPI, batching SeedColdRecords calls so peak transient memory
// stays bounded at the larger scales.
func seedPaired(fatalf func(string, ...any), n int) *CitizensAPI {
	api, err := NewCitizensAPI(1, "tickperf-seed")
	if err != nil {
		fatalf("NewCitizensAPI: %v", err)
	}
	const batchPairs = 100_000 // 200,000 records/batch
	records := make([]ColdRecord, 0, batchPairs*2)
	pairs := n / 2
	for k := 0; k < pairs; k += batchPairs {
		hi := k + batchPairs
		if hi > pairs {
			hi = pairs
		}
		records = records[:0]
		records = buildPairedRecords(2*(hi-k), records)
		// buildPairedRecords assumes ids start at 1; offset ids/partners/
		// households for this batch's true position in the population.
		offset := uint64(2 * k)
		hOffset := uint64(k)
		for i := range records {
			records[i].ID += offset
			if records[i].Partner != 0 {
				records[i].Partner += offset
			}
			records[i].Household += hOffset
		}
		if err := api.SeedColdRecords(records, "tickperf-seed"); err != nil {
			fatalf("SeedColdRecords: %v", err)
		}
	}
	return api
}

func msOf(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000.0
}

func scaleName(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("N=%dM", n/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("N=%dk", n/1_000)
	default:
		return fmt.Sprintf("N=%d", n)
	}
}

// TestAdvanceDayTickCurve (BUG-666) is the deterministic, single-shot
// companion to BenchmarkAdvanceDayTick: it times a full simulated month
// (DaysPerMonth ticks) after one warm-up tick, at each proving-plan scale
// point — the same shape as
// docs/planning/go-engine-100m-proving-plan.md §1.2's own harness ("run
// AdvanceDayTick for a full simulated month (30 ticks) after a warm-up
// tick"), reported as fullMonthMsPerTick below.
//
// It ALSO reports a second, narrower figure, steadyStateMsPerTick: the
// average over ticks 6-25 of the month only, deliberately excluding tick 0
// (registry.go's AdvanceDayTick recomputes monthParams via
// coldParamsLocked -> allColdRecordsLocked, an O(N) full-population
// materialisation — §3.5 of the proving plan, a SEPARATE, already-flagged
// finding, Track-B item 7, not this ticket's fix) and tick 29 (the
// once-per-month death-queue realisation drain). Both of those ticks pay a
// cost that is O(N) or O(D) regardless of BUG-666's fix, so folding them
// into a 30-tick average dilutes the improvement from removing
// ColdShard.rowOf's O(shard size) scan from the PER-TICK fertility path
// (fertility.go's applyFertilityLocked, called every single day-tick).
// steadyStateMsPerTick isolates exactly the cost this fix targets;
// fullMonthMsPerTick is the number directly comparable to the proving
// plan's own table. Population is buildPairedRecords/seedPaired — real
// mutual partner pairs, so the fertility partner lookup this bug fixes
// actually fires every tick (see buildPairedRecords' doc comment for why
// an earlier version of this harness did not).
//
// A fixed-size window (vs. a Go benchmark's adaptive b.N) keeps the
// comparison across scales apples-to-apples. Skipped under -short; run
// with -v to see the per-scale ms/tick table.
func TestAdvanceDayTickCurve(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-scale tick-curve measurement is too slow for -short")
	}
	for _, n := range []int{100_000, 300_000, 1_000_000, 3_000_000} {
		n := n
		t.Run(scaleName(n), func(t *testing.T) {
			api := seedPaired(t.Fatalf, n)
			if _, _, err := api.AdvanceDayTick("curve-warmup"); err != nil {
				t.Fatalf("warm-up AdvanceDayTick: %v", err)
			}
			prevGC := debug.SetGCPercent(-1)
			defer debug.SetGCPercent(prevGC)
			runtime.GC()

			var fullMonth time.Duration
			var steadyState time.Duration
			const steadyFrom, steadyTo = 6, 25 // inclusive tick range, 20 ticks
			for i := 0; i < DaysPerMonth; i++ {
				start := time.Now()
				if _, _, err := api.AdvanceDayTick("curve"); err != nil {
					t.Fatalf("AdvanceDayTick: %v", err)
				}
				d := time.Since(start)
				fullMonth += d
				if i >= steadyFrom && i <= steadyTo {
					steadyState += d
				}
			}
			fullPerTick := fullMonth / DaysPerMonth
			steadyPerTick := steadyState / (steadyTo - steadyFrom + 1)
			t.Logf("N=%d: fullMonthMsPerTick=%.3f steadyStateMsPerTick=%.3f (total %v for %d ticks)",
				n, msOf(fullPerTick), msOf(steadyPerTick), fullMonth, DaysPerMonth)
		})
	}
}

// BenchmarkAdvanceDayTick is the FIRST Go benchmark anywhere in
// internal/engine (BUG-666, Track B item 3 of
// docs/planning/go-engine-100m-proving-plan.md: "grep 'func Benchmark'
// internal/engine/ returns nothing"). It reproduces the proving plan's own
// §1.2 measurement shape at the plan's scale points, using the same
// real mutual-partner-pair population as TestAdvanceDayTickCurve. Run with
// `go test -run NONE -bench AdvanceDayTick -benchtime=5x ./internal/engine/citizens`
// (NONE skips the slow non-bench tests; an explicit -benchtime avoids Go's
// auto-scaling recalibration, where a single day-tick can itself be real
// wall time at the larger scales).
func BenchmarkAdvanceDayTick(b *testing.B) {
	for _, n := range []int{100_000, 300_000, 1_000_000, 3_000_000} {
		b.Run(scaleName(n), func(b *testing.B) {
			api := seedPaired(b.Fatalf, n)
			// One warm-up tick outside the timed loop.
			if _, _, err := api.AdvanceDayTick("bench-warmup"); err != nil {
				b.Fatalf("warm-up AdvanceDayTick: %v", err)
			}
			// GC disabled for the timed loop (mirrors
			// TestColdStore1MRealAllocation's methodology): a stop-the-world
			// GC landing inside one of only a handful of timed iterations
			// (b.N is small at these scales) would attribute an unrelated
			// GC pause to that one iteration's per-tick cost.
			prevGC := debug.SetGCPercent(-1)
			defer debug.SetGCPercent(prevGC)
			runtime.GC()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, _, err := api.AdvanceDayTick("bench"); err != nil {
					b.Fatalf("AdvanceDayTick: %v", err)
				}
			}
		})
	}
}
