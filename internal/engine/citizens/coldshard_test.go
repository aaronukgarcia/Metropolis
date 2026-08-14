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
		Household:       uint32(id / 2),
		Partner:         uint32(id/2 + 1),
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
// per-citizen byte cost falls inside A1's 60-100B band. The figure is
// computed from the real column element types (unsafe.Sizeof), not a
// hardcoded constant (GR#15).
func TestColdShardBytesPerCitizen(t *testing.T) {
	s := newColdShard(0)
	for i := 0; i < 100; i++ {
		s.append(mkRecord(uint64(i+1), uint16(i%10)))
	}
	bpc := s.bytesPerCitizen()
	t.Logf("cold store per-citizen byte cost = %d bytes", bpc)
	if bpc < 60 || bpc > 100 {
		t.Fatalf("cold store %d B/citizen is outside A1's 60-100B band", bpc)
	}
}

// TestColdStore100MProjection (AC-5/US-3): 100M citizens × the measured
// per-citizen cost lands inside A1's 6-10GB band — the naive 25GB that
// motivated the amendment is avoided. GB is decimal (10^9), matching the
// spec's own "25GB at 250B × 100M" arithmetic.
func TestColdStore100MProjection(t *testing.T) {
	bpc := (&ColdShard{}).bytesPerCitizen()
	const hundredM = 100_000_000
	total := uint64(bpc) * hundredM
	const decimalGB = uint64(1000 * 1000 * 1000)
	t.Logf("100M citizens @ %d B/citizen = %.2f GB", bpc, float64(total)/float64(decimalGB))
	if total < 6*decimalGB || total > 10*decimalGB {
		t.Fatalf("100M-citizen cold store = %.2f GB, outside A1's 6-10GB band", float64(total)/float64(decimalGB))
	}
}

// TestColdStore1MRealAllocation (the brief's 1M-citizen SoA proof): 1M
// citizens seeded into the 256 shards measure, via real heap growth, a
// per-citizen cost that matches the arithmetic (within allocator overhead)
// and never exceeds A1's 100B ceiling.
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
	if per > 100 {
		t.Fatalf("cold store %.1f B/citizen exceeds A1's 100B ceiling", per)
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
