package citizens

import (
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// ColdShard is the columnar struct-of-arrays (SoA) cold citizen store for
// one shard (A1, AC-5): every field is a dedicated slice (a column), all
// of the same length (the shard's citizen count), indexed by a single
// row index. Field-level compression:
//
//   - bucketed enums: sex, healthBand, employmentState/sector, education
//     stage, and every personality/satisfaction axis are small integer
//     codes (int8/uint8), never strings;
//   - delta-coded ages: BirthMonth is NOT stored per citizen — only the
//     int16 delta of the citizen's birth month relative to the shard's
//     epochMonth (age is derived, AC-2);
//   - bit/byte-packed states: employment packs state (high 4 bits) and
//     sector (low 4 bits) into one uint8; satisfaction/personality axes
//     are int8; home/workplace/school ids are narrowed to uint32 (2^32 ids
//     is far beyond any city this project targets). household/partner ids
//     are FULL-WIDTH uint64 (BUG-541-class fix, births-unblock lane,
//     2026-09-02): engine.attract mints admitted-migrant ids from 1<<62 and
//     this package's own fertility-born children from 1<<63 (see
//     fertilityChildIDBase's doc comment) — both far outside uint32's
//     range, so narrowing them via safeUint32() silently SATURATED every
//     cross-cohort partner/household reference to math.MaxUint32,
//     permanently zeroing Citizen.Partner for any migrant-or-fertility-
//     child couple and excluding them from FertilityEligible's partnerID==0
//     check. This was the births blocker: with the columns narrowed, births
//     were structurally impossible for anyone outside the closed seed
//     cohort. Widening these two columns only (not workplace/school, which
//     stay uint32 — no known collision there) restores full-width id
//     round-tripping at a modest cost: +8B/citizen (67B -> 75B) for the two
//     widened columns, still comfortably inside A1's 60-100B band.
//
// The columns alone measure ~75B/citizen, inside A1's 60–100B band. BUG-666
// added an id->row index (map[uint64]int32, this struct's `index` field,
// maintained through every append/removeAt) so a lookup is no longer an
// O(shard size) linear scan; the index's real measured cost is ~38B/citizen
// (TestColdShardIndexOverhead — about 3x the 6-8B/citizen back-of-envelope
// estimate that motivated it), so bytesPerCitizen()'s total (columns +
// index) is ~113B, which is OUTSIDE the original 60-100B band — see
// bytesPerCitizen's doc comment and doc.go's byte-budget section for the
// honest revised numbers; this is a real memory-budget finding, not hidden
// behind a silently-widened test. A ColdShard is accessed by row index
// only; rows are removed by swap-with-last (order within a shard is not a
// determinism input — the monthly pass iterates rows in index order, which
// is stable for a fixed population, and the id->row index is LOOKUP ONLY —
// nothing ranges over it to decide tick order, GR#21).
type ColdShard struct {
	// identity + age
	ids        []uint64
	birthDelta []int16 // birthMonth - epochMonth (delta-coded age)
	sexes      []uint8

	// relationships
	households []uint64 // widened from uint32 — births-unblock lane, 2026-09-02 (see ColdShard's doc comment)
	partners   []uint64 // widened from uint32 — births-unblock lane, 2026-09-02 (see ColdShard's doc comment)
	childCount []uint8

	// location / stratification
	homeCells  []uint32
	districts  []uint16
	workplaces []uint32 // workplace ref (0 = none)
	schools    []uint32 // school ref (0 = none)

	// personality (8 axes, int8 columns)
	pSociability   []int8
	pAmbition      []int8
	pConscientious []int8
	pNovelty       []int8
	pPhysicality   []int8
	pCommunity     []int8
	pPatience      []int8
	pAesthetic     []int8

	// education
	attainment []int16
	stages     []uint8
	schooling  []int16

	// health / wealth / employment
	healthBands []uint8
	access      []uint8 // healthcare access, 0-100
	wealth      []int64
	employment  []uint8 // packed: state (high 4 bits) | sector (low 4 bits)

	// satisfaction (5 axes, int8 columns)
	satHousing     []int8
	satServices    []int8
	satEnvironment []int8
	satLeisureFit  []int8
	satCommute     []int8

	// monthly-advance bookkeeping (AC-7's "exactly once per month")
	monthlyUpdates []uint32

	// epochMonth is the shard's age-delta epoch: birthDelta[i] + epochMonth
	// reconstructs the citizen's absolute birth month.
	epochMonth int64

	// index is the id->row lookup this shard was missing (BUG-666): before
	// this, rowOf was a O(shard size) linear scan over ids, which made every
	// single-citizen lookup ~390k comparisons at 100M (256 shards, ~390k
	// rows/shard) and turned applyFertilityLocked (fertility.go) and all
	// four moneycirc.go monthly resident passes quadratic per tick/month,
	// plus the death drain O(D*N/256) (registry.go's realisation loop). This
	// map is maintained through EVERY mutation of the row set — append,
	// removeAt's swap-delete, and rebuildIndexLocked for a shard built by a
	// path that does not go through append (paging.go's wireToColdShard
	// decode) — so it is always exact, never stale. It is a LOOKUP-ONLY
	// structure: nothing in this package ever ranges over it to decide tick
	// order (GR#21) — every iteration that affects simulation state still
	// walks the columnar slices (ids, etc.) in index order, which is
	// map-independent and deterministic.
	index map[uint64]int32
}

// newColdShard constructs an empty shard with the given age-delta epoch.
func newColdShard(epochMonth int64) *ColdShard {
	return &ColdShard{epochMonth: epochMonth, index: make(map[uint64]int32)}
}

// rebuildIndexLocked rebuilds the id->row index from the current ids column.
// Used only by paths that build a ColdShard's columns directly rather than
// via append (paging.go's wireToColdShard, decoding a placeholder gob page
// off disk) — the index is a derived, non-serialized structure, so a shard
// reconstructed from wire data must rebuild it once before any rowOf lookup
// can trust it. A no-op-safe rebuild: always correct regardless of what
// index (if any) the struct literal started with.
func (s *ColdShard) rebuildIndexLocked() {
	s.index = make(map[uint64]int32, len(s.ids))
	for i, id := range s.ids {
		s.index[id] = int32(i)
	}
}

// count returns the number of citizens in the shard (the length of every
// column).
func (s *ColdShard) count() int {
	return len(s.ids)
}

// append adds one cold citizen record to every column.
func (s *ColdShard) append(r ColdRecord) {
	s.ids = append(s.ids, r.ID)
	s.birthDelta = append(s.birthDelta, safeInt16FromInt64(r.BirthMonth-s.epochMonth))
	s.sexes = append(s.sexes, uint8(r.Sex))
	s.households = append(s.households, r.Household)
	s.partners = append(s.partners, r.Partner)
	s.childCount = append(s.childCount, r.ChildCount)
	s.homeCells = append(s.homeCells, uint32(r.Home))
	s.districts = append(s.districts, r.District)
	s.workplaces = append(s.workplaces, r.Workplace)
	s.schools = append(s.schools, r.School)
	s.pSociability = append(s.pSociability, int8(r.Personality[AxisSociability]))
	s.pAmbition = append(s.pAmbition, int8(r.Personality[AxisAmbition]))
	s.pConscientious = append(s.pConscientious, int8(r.Personality[AxisConscientious]))
	s.pNovelty = append(s.pNovelty, int8(r.Personality[AxisNovelty]))
	s.pPhysicality = append(s.pPhysicality, int8(r.Personality[AxisPhysicality]))
	s.pCommunity = append(s.pCommunity, int8(r.Personality[AxisCommunity]))
	s.pPatience = append(s.pPatience, int8(r.Personality[AxisPatience]))
	s.pAesthetic = append(s.pAesthetic, int8(r.Personality[AxisAesthetic]))
	s.attainment = append(s.attainment, r.Attainment)
	s.stages = append(s.stages, uint8(r.Stage))
	s.schooling = append(s.schooling, r.Schooling)
	s.healthBands = append(s.healthBands, uint8(r.HealthBand))
	s.access = append(s.access, r.Access)
	s.wealth = append(s.wealth, r.Wealth)
	s.employment = append(s.employment, packEmployment(r.EmploymentState, r.Sector))
	s.satHousing = append(s.satHousing, safeSat(r.SatHousing))
	s.satServices = append(s.satServices, safeSat(r.SatServices))
	s.satEnvironment = append(s.satEnvironment, safeSat(r.SatEnvironment))
	s.satLeisureFit = append(s.satLeisureFit, safeSat(r.SatLeisureFit))
	s.satCommute = append(s.satCommute, safeSat(r.SatCommute))
	s.monthlyUpdates = append(s.monthlyUpdates, 0)

	if s.index == nil {
		s.index = make(map[uint64]int32, len(s.ids))
	}
	s.index[r.ID] = int32(len(s.ids) - 1)
}

// removeAt deletes row i by swap-with-last, preserving column lengths. The
// id->row index is maintained here too (BUG-666): the removed id's entry is
// deleted, and — unless i was already the last row — the moved-into-place
// id's entry is repointed to i. Captured BEFORE the columns are overwritten,
// since s.ids[i] = s.ids[last] below destroys the pre-swap value at i.
func (s *ColdShard) removeAt(i int) {
	last := s.count() - 1
	removedID := s.ids[i]
	movedID := s.ids[last]

	s.ids[i] = s.ids[last]
	s.birthDelta[i] = s.birthDelta[last]
	s.sexes[i] = s.sexes[last]
	s.households[i] = s.households[last]
	s.partners[i] = s.partners[last]
	s.childCount[i] = s.childCount[last]
	s.homeCells[i] = s.homeCells[last]
	s.districts[i] = s.districts[last]
	s.workplaces[i] = s.workplaces[last]
	s.schools[i] = s.schools[last]
	s.pSociability[i] = s.pSociability[last]
	s.pAmbition[i] = s.pAmbition[last]
	s.pConscientious[i] = s.pConscientious[last]
	s.pNovelty[i] = s.pNovelty[last]
	s.pPhysicality[i] = s.pPhysicality[last]
	s.pCommunity[i] = s.pCommunity[last]
	s.pPatience[i] = s.pPatience[last]
	s.pAesthetic[i] = s.pAesthetic[last]
	s.attainment[i] = s.attainment[last]
	s.stages[i] = s.stages[last]
	s.schooling[i] = s.schooling[last]
	s.healthBands[i] = s.healthBands[last]
	s.access[i] = s.access[last]
	s.wealth[i] = s.wealth[last]
	s.employment[i] = s.employment[last]
	s.satHousing[i] = s.satHousing[last]
	s.satServices[i] = s.satServices[last]
	s.satEnvironment[i] = s.satEnvironment[last]
	s.satLeisureFit[i] = s.satLeisureFit[last]
	s.satCommute[i] = s.satCommute[last]
	s.monthlyUpdates[i] = s.monthlyUpdates[last]

	s.ids = s.ids[:last]
	s.birthDelta = s.birthDelta[:last]
	s.sexes = s.sexes[:last]
	s.households = s.households[:last]
	s.partners = s.partners[:last]
	s.childCount = s.childCount[:last]
	s.homeCells = s.homeCells[:last]
	s.districts = s.districts[:last]
	s.workplaces = s.workplaces[:last]
	s.schools = s.schools[:last]
	s.pSociability = s.pSociability[:last]
	s.pAmbition = s.pAmbition[:last]
	s.pConscientious = s.pConscientious[:last]
	s.pNovelty = s.pNovelty[:last]
	s.pPhysicality = s.pPhysicality[:last]
	s.pCommunity = s.pCommunity[:last]
	s.pPatience = s.pPatience[:last]
	s.pAesthetic = s.pAesthetic[:last]
	s.attainment = s.attainment[:last]
	s.stages = s.stages[:last]
	s.schooling = s.schooling[:last]
	s.healthBands = s.healthBands[:last]
	s.access = s.access[:last]
	s.wealth = s.wealth[:last]
	s.employment = s.employment[:last]
	s.satHousing = s.satHousing[:last]
	s.satServices = s.satServices[:last]
	s.satEnvironment = s.satEnvironment[:last]
	s.satLeisureFit = s.satLeisureFit[:last]
	s.satCommute = s.satCommute[:last]
	s.monthlyUpdates = s.monthlyUpdates[:last]

	delete(s.index, removedID)
	if i != last {
		s.index[movedID] = int32(i)
	}
}

// rowOf returns the row index of id, or -1 if absent. BUG-666: this used to
// be a linear scan over s.ids (O(shard size) — ~390k comparisons per lookup
// at 100M, 256 shards). It is now a single map lookup against the
// id->row index maintained by append/removeAt/rebuildIndexLocked. A nil
// index (a ColdShard zero value, e.g. in a test that never calls append)
// behaves exactly as an empty index: every lookup misses, matching the old
// linear scan's behaviour over an empty ids slice.
func (s *ColdShard) rowOf(id uint64) int {
	if row, ok := s.index[id]; ok {
		return int(row)
	}
	return -1
}

// recordAt reconstructs the cold citizen view at row i (personality
// included — the eight axis columns are widened back into the record).
func (s *ColdShard) recordAt(i int) ColdRecord {
	state, sector := unpackEmployment(s.employment[i])
	return ColdRecord{
		ID:              s.ids[i],
		BirthMonth:      s.epochMonth + int64(s.birthDelta[i]),
		Sex:             Sex(s.sexes[i]),
		Household:       s.households[i],
		Partner:         s.partners[i],
		ChildCount:      s.childCount[i],
		Home:            CellRef(s.homeCells[i]),
		District:        s.districts[i],
		Workplace:       s.workplaces[i],
		School:          s.schools[i],
		Personality:     s.personalityAt(i),
		Attainment:      s.attainment[i],
		Stage:           Stage(s.stages[i]),
		Schooling:       s.schooling[i],
		HealthBand:      HealthBand(s.healthBands[i]),
		Access:          s.access[i],
		Wealth:          s.wealth[i],
		EmploymentState: state,
		Sector:          sector,
		SatHousing:      int32(s.satHousing[i]),
		SatServices:     int32(s.satServices[i]),
		SatEnvironment:  int32(s.satEnvironment[i]),
		SatLeisureFit:   int32(s.satLeisureFit[i]),
		SatCommute:      int32(s.satCommute[i]),
	}
}

// personalityAt widens the eight int8 axis columns into the record's
// personality field.
func (s *ColdShard) personalityAt(i int) [NumPersonalityAxes]int8 {
	return [NumPersonalityAxes]int8{
		s.pSociability[i], s.pAmbition[i], s.pConscientious[i], s.pNovelty[i],
		s.pPhysicality[i], s.pCommunity[i], s.pPatience[i], s.pAesthetic[i],
	}
}

// setPersonality narrows a rich (int32) personality back into the eight
// int8 axis columns at row i. The axes are already 0-100 by validation, so
// the int8 narrowing cannot wrap; it is written here once so every
// write-through path (education drift) goes through the same coercion.
func (s *ColdShard) setPersonality(i int, p Personality) {
	s.pSociability[i] = int8(p[AxisSociability])
	s.pAmbition[i] = int8(p[AxisAmbition])
	s.pConscientious[i] = int8(p[AxisConscientious])
	s.pNovelty[i] = int8(p[AxisNovelty])
	s.pPhysicality[i] = int8(p[AxisPhysicality])
	s.pCommunity[i] = int8(p[AxisCommunity])
	s.pPatience[i] = int8(p[AxisPatience])
	s.pAesthetic[i] = int8(p[AxisAesthetic])
}

// packEmployment packs (state, sector) into one uint8: high 4 bits state,
// low 4 bits sector.
func packEmployment(state EmploymentState, sector Sector) uint8 {
	return uint8(state)<<4 | uint8(sector)&0x0f
}

// unpackEmployment reverses packEmployment.
func unpackEmployment(packed uint8) (EmploymentState, Sector) {
	return EmploymentState(packed >> 4), Sector(packed & 0x0f)
}

// ColdRecord is the reconstructed cold-citizen view handed to the sampler,
// life-writer, and promotion path. It is a value type, never aliased with
// the shard's columns.
type ColdRecord struct {
	ID              uint64
	BirthMonth      int64
	Sex             Sex
	Household       uint64 // widened from uint32 — births-unblock lane, 2026-09-02
	Partner         uint64 // widened from uint32 — births-unblock lane, 2026-09-02
	ChildCount      uint8
	Home            CellRef
	District        uint16
	Workplace       uint32 // workplace ref, 0 = none
	School          uint32 // school ref, 0 = none
	Personality     [NumPersonalityAxes]int8
	Attainment      int16
	Stage           Stage
	Schooling       int16
	HealthBand      HealthBand
	Access          uint8
	Wealth          int64
	EmploymentState EmploymentState
	Sector          Sector
	SatHousing      int32
	SatServices     int32
	SatEnvironment  int32
	SatLeisureFit   int32
	SatCommute      int32
}

// AgeMonths derives the citizen's age at the given month.
func (r ColdRecord) AgeMonths(month int64) int64 {
	return month - r.BirthMonth
}

// coldShardIndexBytesPerCitizen is the measured per-citizen cost of the
// BUG-666 id->row index (map[uint64]int32), amortised over a shard's live
// entries. GR#15: derived from data, not a hand-waved constant — measured
// by TestColdShardIndexOverhead, which builds 256 shard-sized maps the same
// way SeedColdRecords does (one map per shard, grown one insert at a time)
// and diffs real heap growth. The naive key+value sum is
// unsafe.Sizeof(uint64(0))+unsafe.Sizeof(int32(0)) = 12B, but Go's runtime
// hash map (bucketed, tophash byte per slot, ~6.5/8 average load factor,
// overflow-chain slack) measured consistently at ~37.8B/citizen on go1.25
// amd64 — roughly 3x the 6-8B/citizen estimate in
// docs/planning/go-engine-100m-proving-plan.md §3.8, which was a
// back-of-envelope figure, not a measurement. Rounded up to the next whole
// byte from the measured value so this constant is never an
// under-estimate. Pre-sizing the map at construction (make(map[K]V, n))
// was tried and made no measurable difference — see TestColdShardIndexOverhead's
// comment — so the plain construction path (used by append/newColdShard) is
// kept simple.
const coldShardIndexBytesPerCitizen = 38

// bytesPerCitizen is the measured per-citizen byte cost of the columnar
// layout PLUS the BUG-666 id->row index: the sum, over every column, of
// unsafe.Sizeof(column element type), plus coldShardIndexBytesPerCitizen.
// The column part is a RAW field-sum (a lower bound, exactly like
// engine.world's perCellTerrainBytes): it counts the bytes each citizen's
// values occupy, not slice-header bookkeeping or allocator size-class
// rounding, which the real-allocation test (TestColdShardRealAllocation)
// measures separately. Summed over all columns this is the concrete A1
// arithmetic, never a hand-waved number. The index part IS a measured
// constant (see coldShardIndexBytesPerCitizen's derivation) because a Go
// map's real per-entry cost has no clean unsafe.Sizeof equivalent — there
// is no single "map bucket" type to size.
//
// BUG-666 note: the combined total (~113B) exceeds doc.go's original A1
// 60-100B band. That band was set before this index existed; the
// tests below assert the REAL measured number (GR#15), not the stale
// band, and doc.go's byte-budget section is updated in the same commit to
// say so plainly rather than silently widen the assertion to hide the
// miss. This is a genuine memory-budget finding for Aaron, not a defect in
// this fix: the fix's own bar is the tick-time curve (§3 of the proving
// plan), and a 113B/citizen cold store is still ~11.3GB at 100M, half of
// the 32GiB floor the proving plan already sizes for.
func (s *ColdShard) bytesPerCitizen() int {
	var u64 uint64
	var i16 int16
	var u8 uint8
	var u32 uint32
	var u16 uint16
	var i8 int8
	var i64 int64

	total := unsafe.Sizeof(u64) // ids
	total += unsafe.Sizeof(i16) // birthDelta
	total += unsafe.Sizeof(u8)  // sexes
	total += unsafe.Sizeof(u64) // households (widened from uint32 — births-unblock lane, 2026-09-02)
	total += unsafe.Sizeof(u64) // partners (widened from uint32 — births-unblock lane, 2026-09-02)
	total += unsafe.Sizeof(u8)  // childCount
	total += unsafe.Sizeof(u32) // homeCells
	total += unsafe.Sizeof(u16) // districts
	total += unsafe.Sizeof(u32) // workplaces
	total += unsafe.Sizeof(u32) // schools
	// 8 personality axes
	for i := 0; i < NumPersonalityAxes; i++ {
		total += unsafe.Sizeof(i8)
	}
	total += unsafe.Sizeof(i16) // attainment
	total += unsafe.Sizeof(u8)  // stages
	total += unsafe.Sizeof(i16) // schooling
	total += unsafe.Sizeof(u8)  // healthBands
	total += unsafe.Sizeof(u8)  // access
	total += unsafe.Sizeof(i64) // wealth
	total += unsafe.Sizeof(u8)  // employment
	// 5 satisfaction axes
	for i := 0; i < NumSatisfactionComponents; i++ {
		total += unsafe.Sizeof(i8)
	}
	total += unsafe.Sizeof(u32) // monthlyUpdates
	return int(total) + coldShardIndexBytesPerCitizen
}

// validateShardIndex returns a registry-sourced error for an out-of-range
// shard index (GR#1) rather than an out-of-bounds slice panic.
func validateShardIndex(shard, numShards int, correlationID string) error {
	if shard < 0 || shard >= numShards {
		return errs.New(ErrShardIndexOutOfRange, correlationID, map[string]any{
			"shard":     shard,
			"numShards": numShards,
		})
	}
	return nil
}
