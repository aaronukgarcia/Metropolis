package citizens

import (
	"reflect"
	"runtime"
	"runtime/debug"
	"testing"
)

// mkRecord builds a deterministic cold record for tests.
func mkRecord(id uint64, district uint16) ColdRecord {
	var p [NumPersonalityAxes]int8
	for i := range p {
		p[i] = int8(int(id)%100) + int8(i) - 4 // vary deterministically
		if p[i] < 0 {
			p[i] = 0
		}
		if p[i] > 100 {
			p[i] = 100
		}
	}
	return ColdRecord{
		ID:              id,
		BirthMonth:      int64(id % 1200), // within 100 years of genesis
		Sex:             Sex(id % 2),
		Household:       id / 2,
		Partner:         id/2 + 1,
		ChildCount:      uint8(id % 4),
		Home:            CellRef(id % 1_000_000),
		District:        district,
		Workplace:       uint32(id % 500),
		School:          uint32(id % 200),
		Personality:     p,
		Attainment:      int16(id % 100),
		Stage:           Stage(id % 8),
		Schooling:       int16(id % 200),
		HealthBand:      HealthBand(id % 6),
		Access:          uint8(id % 101),
		Wealth:          int64(id * 1000),
		EmploymentState: EmploymentState(id % 5),
		Sector:          Sector(id % 5),
		SatHousing:      int32(id % 100),
		SatServices:     int32(id % 100),
		SatEnvironment:  int32(id % 100),
		SatLeisureFit:   int32(id % 100),
		SatCommute:      int32(id % 100),
	}
}

// TestColdShardBytesPerCitizen (AC-5): the columnar SoA layout's measured
// per-citizen byte cost, INCLUDING the BUG-666 id->row index. The figure is
// computed from the real column element types (unsafe.Sizeof) plus the
// measured index constant (coldShardIndexBytesPerCitizen, see
// TestColdShardIndexOverhead), not a hardcoded guess (GR#15). BUG-666: the
// real total (~113B) is now OUTSIDE doc.go's original A1 60-100B band — the
// band was set before this index existed, and TestColdShardIndexOverhead
// proved the index's real Go-map cost (~38B/citizen) is ~3x the 6-8B/citizen
// estimate the proving plan guessed. The assertion below checks the real
// measured band (60-120B), not the stale one, and doc.go's byte-budget
// section says so plainly — this is a genuine memory-budget finding for
// Aaron, not something to hide behind a silently-widened test.
func TestColdShardBytesPerCitizen(t *testing.T) {
	s := newColdShard(0)
	for i := 0; i < 100; i++ {
		s.append(mkRecord(uint64(i+1), uint16(i%10)))
	}
	bpc := s.bytesPerCitizen()
	t.Logf("cold store per-citizen byte cost (columns + BUG-666 index) = %d bytes", bpc)
	if bpc < 60 || bpc > 120 {
		t.Fatalf("cold store %d B/citizen is outside the post-BUG-666 60-120B band", bpc)
	}
}

// TestColdShardIndexOverhead (BUG-666): measures the REAL per-citizen cost
// of the id->row index (map[uint64]int32) via actual heap growth, exactly
// the way SeedColdRecords populates it — 256 independently-grown shard
// maps, one insert at a time, never pre-sized (a separate run comparing
// pre-sized vs grow-from-zero maps showed no measurable difference, so the
// simpler unsized construction in newColdShard/append is kept). This is the
// data coldShardIndexBytesPerCitizen's derivation comment cites (GR#15):
// the naive key(8B)+value(4B) sum is 12B, but Go's runtime hash map
// (bucketed, tophash byte per slot, ~6.5/8 average load factor, overflow
// chains) measured consistently at ~37.8B/citizen on go1.25 amd64 — about
// 3x the proving plan's 6-8B/citizen back-of-envelope estimate.
func TestColdShardIndexOverhead(t *testing.T) {
	if testing.Short() {
		t.Skip("index-overhead measurement is too slow for -short")
	}
	const shards = numColdShards
	const n = 1_000_000

	prevGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(prevGC)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	maps := make([]map[uint64]int32, shards)
	for i := range maps {
		maps[i] = make(map[uint64]int32)
	}
	for i := 0; i < n; i++ {
		id := uint64(i + 1)
		sh := i % shards
		maps[sh][id] = int32(len(maps[sh]))
	}

	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(maps)
	actual := after.HeapAlloc - before.HeapAlloc
	per := float64(actual) / n
	t.Logf("BUG-666 index: %d bytes for %d entries across %d shards (%.1f B/citizen) vs the naive 12B key+value sum", actual, n, shards, per)
	if per < 12 {
		t.Fatalf("measured index overhead %.1f B/citizen is below the impossible 12B key+value floor — measurement is broken", per)
	}
	if per > 60 {
		t.Fatalf("measured index overhead %.1f B/citizen is far above the ~38B this package assumes (coldShardIndexBytesPerCitizen) — re-derive the constant", per)
	}
}

// TestColdStore100MProjection (AC-5/US-3): 100M citizens × the measured
// per-citizen cost (columns + BUG-666 index). BUG-666 pushed this above the
// spec's original 6-10GB band (the naive 25GB "~250B/citizen" figure the
// band was originally sized to avoid is still comfortably avoided — see
// doc.go's byte-budget section for the honest revised numbers). GB is
// decimal (10^9), matching the spec's own "25GB at 250B x 100M" arithmetic.
func TestColdStore100MProjection(t *testing.T) {
	bpc := (&ColdShard{}).bytesPerCitizen()
	const hundredM = 100_000_000
	total := uint64(bpc) * hundredM
	const decimalGB = uint64(1000 * 1000 * 1000)
	t.Logf("100M citizens @ %d B/citizen (columns + BUG-666 index) = %.2f GB", bpc, float64(total)/float64(decimalGB))
	if total < 6*decimalGB || total > 15*decimalGB {
		t.Fatalf("100M-citizen cold store = %.2f GB, outside the post-BUG-666 6-15GB band (the naive-250B 25GB figure it must still stay well clear of)", float64(total)/float64(decimalGB))
	}
}

// TestColdStore1MRealAllocation (the brief's 1M-citizen SoA proof): 1M
// citizens seeded into the 256 shards measure, via real heap growth, a
// per-citizen cost that matches the arithmetic (within allocator overhead).
// BUG-666: the ceiling below was A1's original 100B; the BUG-666 index
// measurably pushed real allocation to ~125B/citizen (see this test's own
// t.Logf and TestColdShardIndexOverhead's derivation) — the ceiling is
// raised to 140B (measured + headroom) rather than silently dropped, and
// doc.go's byte-budget section records the same honest number.
func TestColdStore1MRealAllocation(t *testing.T) {
	if testing.Short() {
		t.Skip("1M-citizen seed is too slow for -short")
	}
	const n = 1_000_000
	api, err := NewCitizensAPI(1, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}

	prevGC := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(prevGC)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)

	for i := 0; i < n; i++ {
		api.cold[i%numColdShards].append(mkRecord(uint64(i+1), uint16(i%100)))
	}

	// Collect the transient growth buffers the one-at-a-time appends left
	// behind (with GC disabled they would otherwise be counted, since
	// nothing reclaims them) so the measurement reflects LIVE columns, not
	// the cumulative size of every intermediate slice growth step.
	runtime.GC()
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(api)
	actual := after.HeapAlloc - before.HeapAlloc
	per := float64(actual) / n
	bpc := (&ColdShard{}).bytesPerCitizen()
	t.Logf("1M citizens: real heap growth %d bytes (%.1f B/citizen) vs arithmetic %d B/citizen", actual, per, bpc)

	if actual < uint64(bpc)*n {
		t.Fatalf("real allocation (%d bytes) is less than the arithmetic lower bound (%d bytes)", actual, bpc*n)
	}
	if per > float64(bpc)*2 {
		t.Fatalf("real per-citizen cost %.1f B is >2x the arithmetic %d B — unexpected extra allocation", per, bpc)
	}
	if per > 140 {
		t.Fatalf("cold store %.1f B/citizen exceeds the post-BUG-666 140B ceiling", per)
	}
}

// TestColdPassSchedule (AC-6): the fixed rolling schedule processes a
// balanced 1/30 slice of the 256 shards per day-tick, every shard exactly
// once across the 30-day month, deterministically.
func TestColdPassSchedule(t *testing.T) {
	seen := make(map[int]bool, numColdShards)
	lo := numColdShards / DaysPerMonth
	hi := lo + 1
	for d := 0; d < DaysPerMonth; d++ {
		shards := ColdPassSchedule(d)
		if len(shards) < lo || len(shards) > hi {
			t.Fatalf("day %d scheduled %d shards, want %d or %d", d, len(shards), lo, hi)
		}
		for _, s := range shards {
			if seen[s] {
				t.Fatalf("shard %d scheduled twice across the month", s)
			}
			seen[s] = true
		}
		if !reflect.DeepEqual(shards, ColdPassSchedule(d)) {
			t.Fatalf("ColdPassSchedule(%d) is not deterministic", d)
		}
	}
	if len(seen) != numColdShards {
		t.Fatalf("scheduled %d unique shards across the month, want %d", len(seen), numColdShards)
	}
}

// TestColdCitizensAdvanceOncePerMonth (AC-7): after a full 30-day-tick
// month, every cold citizen's monthly-update counter is exactly 1 — the
// amortisation changes *when*, never *how many times*.
func TestColdCitizensAdvanceOncePerMonth(t *testing.T) {
	api, err := NewCitizensAPI(7, "corr")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	const n = 2000
	records := make([]ColdRecord, n)
	for i := range records {
		records[i] = mkRecord(uint64(i+1), uint16(i%10))
		records[i].BirthMonth = 0 // born at genesis, so age is non-negative at month 0
	}
	if err := api.SeedColdRecords(records, "corr"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("AdvanceMonth: %v", err)
	}
	for shard := 0; shard < numColdShards; shard++ {
		s := api.cold[shard]
		for i := 0; i < s.count(); i++ {
			if s.monthlyUpdates[i] != 1 {
				t.Fatalf("shard %d citizen %d advanced %d times in one month, want exactly 1", shard, s.ids[i], s.monthlyUpdates[i])
			}
		}
	}
	// A second month advances them exactly once more (counter == 2).
	if err := api.AdvanceMonth("corr"); err != nil {
		t.Fatalf("second AdvanceMonth: %v", err)
	}
	for shard := 0; shard < numColdShards; shard++ {
		s := api.cold[shard]
		for i := 0; i < s.count(); i++ {
			if s.monthlyUpdates[i] != 2 {
				t.Fatalf("shard %d citizen %d advanced %d times in two months, want exactly 2", shard, s.ids[i], s.monthlyUpdates[i])
			}
		}
	}
}
