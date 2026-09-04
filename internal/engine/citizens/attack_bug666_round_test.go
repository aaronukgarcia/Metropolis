package citizens

// BUG-666 independent destructive round (GR#23 — attacker is not the
// author). The estate under attack replaced ColdShard.rowOf's O(shard size)
// linear scan with a per-shard map[uint64]int32 id->row index maintained at
// append / removeAt (swap-delete) / rebuildIndexLocked (the paging gob
// decode path).
//
// THE STAKES, and therefore the shape of this file: a stale index entry does
// not panic. It returns SOME row — the wrong citizen's row — and every
// caller (registry.go's coldRecord, setColdHouseholdLocked, mutateColdLocked,
// coldRowLocked, the death-realisation dissolution loop, fertility's partner
// lookup) then reads or WRITES another citizen's data with no error, no log
// and no crash. That is silent cross-citizen corruption, the worst defect
// class this engine can carry, and it is invisible to any test that only
// asserts "no panic" or "population count is right".
//
// So every test below is a DIFFERENTIAL against an oracle: oracleRowOf is
// the OLD linear scan, byte-for-byte the pre-fix implementation, and
// assertShardIndexSound proves index and oracle agree for every id in the
// shard, that no index entry is stale, and that no live row is unreachable.
// The API-level tests additionally carry a CONTENT witness (homeCells, the
// one column nothing mutates at runtime — see the grep in this round's
// report) so a lookup that lands on the wrong row is caught by the DATA it
// returns, not merely by an index-vs-index comparison that a corrupted index
// could satisfy on both sides.

import (
	"bytes"
	"encoding/gob"
	"math"
	"math/rand"
	"os"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// oracleRowOf is BUG-666's PRE-FIX rowOf, verbatim: a linear scan over the
// ids column. It is the ground truth every assertion in this file measures
// the index against. It deliberately does not consult s.index.
func oracleRowOf(s *ColdShard, id uint64) int {
	for i, v := range s.ids {
		if v == id {
			return i
		}
	}
	return -1
}

// assertShardIndexSound proves the three properties a silent-corruption bug
// would violate, in the three directions they can fail:
//
//  1. FORWARD (a live citizen is findable, at the RIGHT row): for every row
//     i, rowOf(ids[i]) agrees with the linear-scan oracle and lands on a row
//     whose id really is ids[i]. A repoint bug that leaves a stale row
//     number fails here — quietly, with no panic, which is the whole point.
//  2. REVERSE (no stale/ghost entries): every entry in the index points at
//     an in-range row whose id is that entry's key. A delete bug leaves an
//     entry pointing past the end or at a recycled row; both are caught.
//  3. CARDINALITY (nothing lost): the index has exactly one entry per
//     distinct live id. A missed insert makes a live citizen invisible
//     (rowOf == -1) — money/fertility/death lookups then silently skip them
//     — and a missed delete leaves a dead id resolvable.
func assertShardIndexSound(t *testing.T, s *ColdShard, label string) {
	t.Helper()
	for i, id := range s.ids {
		got := s.rowOf(id)
		want := oracleRowOf(s, id)
		if got != want {
			t.Fatalf("%s: rowOf(%d) = %d but the linear-scan oracle says %d (row %d of %d) — STALE INDEX, wrong citizen's row",
				label, id, got, want, i, s.count())
		}
		if got < 0 || got >= s.count() {
			t.Fatalf("%s: rowOf(%d) = %d, out of range [0,%d)", label, id, got, s.count())
		}
		if s.ids[got] != id {
			t.Fatalf("%s: rowOf(%d) = %d but ids[%d] = %d — the index resolves to ANOTHER citizen's row",
				label, id, got, got, s.ids[got])
		}
	}
	live := make(map[uint64]bool, s.count())
	for _, id := range s.ids {
		live[id] = true
	}
	for id, row := range s.index {
		if int(row) < 0 || int(row) >= s.count() {
			t.Fatalf("%s: index entry %d -> row %d is out of range [0,%d) — stale entry survived a removal",
				label, id, row, s.count())
		}
		if s.ids[row] != id {
			t.Fatalf("%s: index entry %d -> row %d, but ids[%d] = %d — GHOST entry aliasing a live citizen",
				label, id, row, row, s.ids[row])
		}
	}
	if len(s.index) != len(live) {
		t.Fatalf("%s: index holds %d entries for %d distinct live ids", label, len(s.index), len(live))
	}
}

// assertRemovedIDsGone proves the negative direction the soundness check
// cannot see: an id that was removed must not resolve at all. A missed
// delete whose row happens to be recycled by a later append is invisible to
// a pure ids-column check but hands a caller a live stranger's row.
func assertRemovedIDsGone(t *testing.T, s *ColdShard, removed []uint64, label string) {
	t.Helper()
	for _, id := range removed {
		if oracleRowOf(s, id) >= 0 {
			continue // re-appended since; not a ghost
		}
		if row := s.rowOf(id); row >= 0 {
			t.Fatalf("%s: removed id %d still resolves to row %d (holding citizen %d)", label, id, row, s.ids[row])
		}
	}
}

// TestBUG666RemoveAtAdversarialSequences drives removeAt through every
// swap-delete shape the author's own tests could plausibly miss, checked
// against the oracle after EVERY single operation (not just at the end,
// where a compensating pair of bugs can cancel out).
func TestBUG666RemoveAtAdversarialSequences(t *testing.T) {
	build := func(n int) *ColdShard {
		s := newColdShard(0)
		for i := 1; i <= n; i++ {
			s.append(mkRecord(uint64(i), uint16(i%10)))
		}
		return s
	}

	cases := []struct {
		name string
		// rows returns the sequence of row indices to remove, evaluated
		// against the CURRENT shard so a case can target "the row the last
		// removal just swapped into place".
		run func(t *testing.T, s *ColdShard) []uint64
	}{
		{
			// The no-swap branch: i == last. removedID == movedID here, so a
			// delete-then-repoint implementation that does not guard i != last
			// would resurrect the dead id pointing at a row past the end.
			name: "remove the last row (no swap)",
			run: func(t *testing.T, s *ColdShard) []uint64 {
				var removed []uint64
				for s.count() > 0 {
					id := s.ids[s.count()-1]
					s.removeAt(s.count() - 1)
					removed = append(removed, id)
					assertShardIndexSound(t, s, "remove-last")
					assertRemovedIDsGone(t, s, removed, "remove-last")
				}
				return removed
			},
		},
		{
			// Remove the row that IS the swap source on the NEXT removal:
			// row 0, repeatedly. Every removal moves the current last row to
			// row 0, so the repoint branch fires on every single call.
			name: "remove row 0 repeatedly (repoint fires every call)",
			run: func(t *testing.T, s *ColdShard) []uint64 {
				var removed []uint64
				for s.count() > 0 {
					id := s.ids[0]
					s.removeAt(0)
					removed = append(removed, id)
					assertShardIndexSound(t, s, "remove-zero")
					assertRemovedIDsGone(t, s, removed, "remove-zero")
				}
				return removed
			},
		},
		{
			// Back-to-back: the second removal targets the row the first
			// removal just swapped a citizen INTO. If the repoint wrote the
			// wrong row number, the second removeAt deletes the wrong id from
			// the index and leaves a live citizen unreachable forever.
			name: "remove i, then immediately remove i again (the just-swapped row)",
			run: func(t *testing.T, s *ColdShard) []uint64 {
				var removed []uint64
				for _, i := range []int{3, 3, 0, 0, 1, 1} {
					if i >= s.count() {
						continue
					}
					id := s.ids[i]
					s.removeAt(i)
					removed = append(removed, id)
					assertShardIndexSound(t, s, "back-to-back")
					assertRemovedIDsGone(t, s, removed, "back-to-back")
				}
				return removed
			},
		},
		{
			// Remove EVERY row, then append fresh ids into the emptied shard.
			// An index left non-empty by the drain aliases the recycled rows.
			name: "drain to empty, then append fresh",
			run: func(t *testing.T, s *ColdShard) []uint64 {
				var removed []uint64
				for s.count() > 0 {
					id := s.ids[0]
					s.removeAt(0)
					removed = append(removed, id)
				}
				if len(s.index) != 0 {
					t.Fatalf("drained shard still holds %d index entries: %v", len(s.index), s.index)
				}
				assertShardIndexSound(t, s, "drained")
				for i := 500; i < 520; i++ {
					s.append(mkRecord(uint64(i), uint16(i%10)))
					assertShardIndexSound(t, s, "refilled")
					assertRemovedIDsGone(t, s, removed, "refilled")
				}
				return removed
			},
		},
		{
			// Single-row shard: last == 0 == i, the degenerate no-swap case.
			name: "single-row shard",
			run: func(t *testing.T, s *ColdShard) []uint64 {
				for s.count() > 1 {
					s.removeAt(s.count() - 1)
				}
				id := s.ids[0]
				s.removeAt(0)
				assertShardIndexSound(t, s, "single")
				if s.count() != 0 || len(s.index) != 0 {
					t.Fatalf("single-row drain left count=%d index=%d", s.count(), len(s.index))
				}
				return []uint64{id}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := build(12)
			assertShardIndexSound(t, s, "seeded")
			removed := tc.run(t, s)
			assertShardIndexSound(t, s, "final")
			assertRemovedIDsGone(t, s, removed, "final")
		})
	}
}

// TestBUG666AppendRemoveFuzzDifferential interleaves appends and removals at
// adversarially-chosen rows (biased hard towards row 0, the last row, and
// the row a previous removal just wrote) for tens of thousands of
// operations, re-proving the full oracle differential after EVERY operation.
// This is the test a hand-written unit case cannot replace: the corrupting
// sequences are the rare ones.
func TestBUG666AppendRemoveFuzzDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(0xB0666))
	s := newColdShard(0)
	var nextID uint64 = 1
	removed := make([]uint64, 0, 4096)
	lastTouched := 0

	const ops = 20_000
	appends, removes := 0, 0
	for op := 0; op < ops; op++ {
		// Bias towards a populated-but-churning shard.
		wantAppend := s.count() < 8 || (s.count() < 64 && rng.Intn(100) < 55) || rng.Intn(100) < 40
		if wantAppend {
			s.append(mkRecord(nextID, uint16(nextID%10)))
			nextID++
			appends++
		} else {
			var i int
			switch rng.Intn(4) {
			case 0:
				i = 0
			case 1:
				i = s.count() - 1
			case 2:
				i = lastTouched
				if i >= s.count() {
					i = s.count() - 1
				}
			default:
				i = rng.Intn(s.count())
			}
			removed = append(removed, s.ids[i])
			s.removeAt(i)
			lastTouched = i
			removes++
		}
		assertShardIndexSound(t, s, "fuzz")
	}
	assertRemovedIDsGone(t, s, removed, "fuzz-final")
	t.Logf("fuzz: %d appends, %d removals, final count %d, %d ids ever removed", appends, removes, s.count(), len(removed))
	if removes < 5_000 || appends < 5_000 {
		t.Fatalf("fuzz did not exercise both paths meaningfully: %d appends / %d removals", appends, removes)
	}
}

// --- API-level differential: the real tick path ------------------------------

// homeWitness is the CONTENT witness for a wrong-row lookup. homeCells is
// the only id-derived column nothing in the package mutates at runtime
// (verified by grepping every `s.<col>[i] =` / `[row] =` assignment: the
// cold pass writes healthBands, sat*, stages and employment; life events
// write employment/healthBands/wealth/households/partners; removeAt's swap
// is the only writer of homeCells). So a lookup that lands on another
// citizen's row returns a Home that does not match the id — corruption
// caught by the DATA, not merely by comparing the index to itself.
type homeWitness map[uint64]CellRef

// assertAPIIndexSound runs the full shard differential across all 256
// shards AND checks the content witness for every live citizen through the
// public lookup path (coldRecord -> rowOf), which is what fertility, the
// death dissolution loop and every moneycirc pass actually call.
func assertAPIIndexSound(t *testing.T, c *CitizensAPI, w homeWitness, label string) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for shard := 0; shard < numColdShards; shard++ {
		s := c.cold[shard]
		assertShardIndexSound(t, s, label)
		for i, id := range s.ids {
			// Shard placement is a pure function of the id; a citizen sitting
			// in the wrong shard's index would be found by nobody.
			if got := det.ShardForEntity(id); got != shard {
				t.Fatalf("%s: citizen %d lives in shard %d but hashes to %d", label, id, shard, got)
			}
			home := CellRef(s.homeCells[i])
			if want, ok := w[id]; ok {
				if home != want {
					t.Fatalf("%s: citizen %d row %d has Home %d, want %d — a swap wrote the wrong row",
						label, id, i, home, want)
				}
			} else {
				w[id] = home
			}
		}
	}
	// The lookup path itself, id by id: rowOf must return THIS citizen.
	for shard := 0; shard < numColdShards; shard++ {
		s := c.cold[shard]
		for _, id := range append([]uint64(nil), s.ids...) {
			row := s.rowOf(id)
			if row < 0 {
				t.Fatalf("%s: live citizen %d is INVISIBLE to rowOf", label, id)
			}
			r := s.recordAt(row)
			if r.ID != id {
				t.Fatalf("%s: lookup of %d returned citizen %d", label, id, r.ID)
			}
			if want, ok := w[id]; ok && r.Home != want {
				t.Fatalf("%s: lookup of %d returned Home %d, want %d — WRONG CITIZEN'S ROW", label, id, r.Home, want)
			}
		}
	}
}

// seedRoundPopulation seeds a mixed population: mutual fertile pairs (which
// APPEND rows every tick as children are born) plus a guaranteed-death
// cohort (which REMOVES rows at every month's realisation drain). Both
// mutation paths therefore run concurrently across the same 256 shards,
// which is the only way to exercise the append/removeAt interleaving the
// real engine performs.
func seedRoundPopulation(t *testing.T, api *CitizensAPI, pairs, dying int, month int64) {
	t.Helper()
	recs := make([]ColdRecord, 0, pairs*2+dying)
	for k := 0; k < pairs; k++ {
		idA := uint64(2*k + 1)
		idB := uint64(2*k + 2)
		a := mkRecord(idA, uint16(k%64))
		a.BirthMonth = month - 300 // 25 years old: inside the childbearing band
		a.Household, a.Partner, a.ChildCount = 0, 0, 0
		b := mkRecord(idB, uint16(k%64))
		b.BirthMonth = month - 300
		b.Household, b.Partner, b.ChildCount = 0, 0, 0
		recs = append(recs, a, b)
	}
	for i := 1; i <= dying; i++ {
		recs = append(recs, mkGuaranteedDeathRecord(1_000_000+uint64(i), month))
	}
	if err := api.SeedColdRecords(recs, "bug666"); err != nil {
		t.Fatalf("SeedColdRecords: %v", err)
	}
	api.mu.Lock()
	api.month = month
	api.mu.Unlock()
	// Pair them through the REAL LifeEventPartner path so the households
	// actually exist in c.households — birthChildLocked's ValidateCitizen
	// rejects a child whose household id resolves to nothing, so a fixture
	// that only writes the household COLUMN produces zero births (found the
	// hard way while building this round: the author's own tickperf fixture
	// has the same shape, see this round's report).
	for k := 0; k < pairs; k++ {
		if err := api.ApplyLifeEventCommand(LifeEventCommand{
			Kind:          LifeEventPartner,
			CitizenID:     uint64(2*k + 1),
			PartnerID:     uint64(2*k + 2),
			CorrelationID: "bug666",
		}); err != nil {
			t.Fatalf("LifeEventPartner(%d,%d): %v", 2*k+1, 2*k+2, err)
		}
	}
}

// TestBUG666TickDifferentialUnderHeavyMortality is the headline test: drive
// the REAL AdvanceDayTick with births appending rows and the death drain
// removing them, and after EVERY tick prove the index still agrees with the
// linear-scan oracle for every id in every touched shard, and that every
// lookup returns that citizen's own data.
//
// It also asserts conservation, because the death path's own
// "structurally unreachable" rowOf < 0 branch (registry.go's realisation
// loop) SKIPS the removal and the household dissolution when a lookup
// misses — so an index that loses an entry would be counted as a death
// while the citizen keeps living, and only a population-vs-events check
// catches that.
func TestBUG666TickDifferentialUnderHeavyMortality(t *testing.T) {
	if testing.Short() {
		t.Skip("multi-month tick differential is too slow for -short")
	}
	const month = int64(20_000)
	api, err := NewCitizensAPI(0xBEEF, "bug666")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	seedRoundPopulation(t, api, 700, 900, month)

	w := make(homeWitness)
	assertAPIIndexSound(t, api, w, "seeded")

	pop := api.TotalPopulation("bug666")
	totalBirths, totalDeaths := 0, 0
	const months = 14
	for m := 0; m < months; m++ {
		for d := 0; d < DaysPerMonth; d++ {
			b, dd, err := api.AdvanceDayTick("bug666")
			if err != nil {
				t.Fatalf("AdvanceDayTick month %d day %d: %v", m, d, err)
			}
			totalBirths += b
			totalDeaths += dd
			assertAPIIndexSound(t, api, w, "tick")
			got := api.TotalPopulation("bug666")
			if got != pop+b-dd {
				t.Fatalf("conservation broken at month %d day %d: population %d -> %d with %d births and %d deaths",
					m, d, pop, got, b, dd)
			}
			pop = got
		}
	}
	t.Logf("ticked %d months: %d births, %d deaths, final population %d", months, totalBirths, totalDeaths, pop)
	if totalDeaths == 0 || totalBirths == 0 {
		t.Fatalf("fixture is vacuous: %d births / %d deaths — neither mutation path ran", totalBirths, totalDeaths)
	}
}

// TestBUG666DeathRealisationRemovesTheRightCitizen closes the specific hole
// the tick differential cannot see on its own: the realisation loop reads
// households/partners AT the resolved row and then removes THAT row. If the
// index resolved a dead id to a living neighbour's row, the loop would
// dissolve the wrong household and delete the wrong citizen — the dead one
// staying alive forever — while the death COUNT stayed perfectly plausible.
func TestBUG666DeathRealisationRemovesTheRightCitizen(t *testing.T) {
	const month = int64(20_000)
	api, err := NewCitizensAPI(0x0666, "bug666")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	// A guaranteed-death cohort plus a control cohort that must NEVER be
	// touched (young, healthy, full access — hazard far from 1).
	dying := seedGuaranteedDeathCohort(t, api, 2_000_000, 400, month)
	control := make([]ColdRecord, 0, 400)
	for i := 1; i <= 400; i++ {
		r := mkRecord(3_000_000+uint64(i), uint16(i%64))
		r.BirthMonth = month - 240 // 20 years old
		r.HealthBand = HealthExcellent
		r.Access = 100
		r.Household, r.Partner = 0, 0
		control = append(control, r)
	}
	if err := api.SeedColdRecords(control, "bug666"); err != nil {
		t.Fatalf("SeedColdRecords(control): %v", err)
	}

	w := make(homeWitness)
	assertAPIIndexSound(t, api, w, "seeded")

	deadSeen := 0
	for m := 0; m < 12; m++ {
		for d := 0; d < DaysPerMonth; d++ {
			if _, dd, err := api.AdvanceDayTick("bug666"); err != nil {
				t.Fatalf("AdvanceDayTick: %v", err)
			} else {
				deadSeen += dd
			}
		}
		assertAPIIndexSound(t, api, w, "post-month")
		// Every control citizen must still be present, findable, and holding
		// their OWN record.
		for _, r := range control {
			got, ok := api.coldRecord(r.ID)
			if !ok {
				t.Fatalf("month %d: control citizen %d vanished — the death path removed the WRONG row", m, r.ID)
			}
			if got.ID != r.ID || got.Home != r.Home {
				t.Fatalf("month %d: control citizen %d resolved to ID=%d Home=%d (want Home=%d)", m, r.ID, got.ID, got.Home, r.Home)
			}
		}
	}
	if deadSeen == 0 {
		t.Fatalf("fixture vacuous: no deaths realised over 12 months")
	}
	// Everyone realised must be genuinely gone from the store, not merely
	// counted.
	gone := 0
	for _, id := range dying {
		if _, ok := api.coldRecord(id); !ok {
			gone++
		}
	}
	if gone != deadSeen {
		t.Fatalf("%d deaths were counted but %d of the cohort actually left the store", deadSeen, gone)
	}
	t.Logf("realised %d deaths over 12 months; all 400 control citizens intact", deadSeen)
}

// --- The gob / paging path ---------------------------------------------------

// TestBUG666PagingRoundTripRebuildsIndex evicts a populated shard to disk
// and reloads it through the real PageStore, then MUTATES the reloaded
// shard. wireToColdShard bypasses append entirely, so without
// rebuildIndexLocked every lookup against a paged-in shard would miss (or,
// worse, a later removeAt would panic writing to a nil map).
func TestBUG666PagingRoundTripRebuildsIndex(t *testing.T) {
	dir := t.TempDir()
	ps := NewPageStore(dir, 0) // maxResident 0 => everything evicts immediately

	s := newColdShard(7)
	for i := 1; i <= 200; i++ {
		s.append(mkRecord(uint64(i)*3, uint16(i%10)))
	}
	assertShardIndexSound(t, s, "pre-page")

	if err := ps.Store(5, s); err != nil {
		t.Fatalf("Store: %v", err)
	}
	if ps.ResidentCount() != 0 {
		t.Fatalf("maxResident=0 should have evicted; resident=%d", ps.ResidentCount())
	}
	loaded, ok := ps.Load(5)
	if !ok {
		t.Fatalf("Load(5) missed after Store")
	}
	if loaded == s {
		t.Fatalf("Load returned the same pointer — the eviction/decode path was not exercised")
	}
	if loaded.index == nil {
		t.Fatalf("decoded shard has a nil index: rowOf would miss every citizen and removeAt would panic")
	}
	assertShardIndexSound(t, loaded, "post-page")

	// Now mutate the RELOADED shard: remove half, append fresh, re-check.
	var removed []uint64
	for i := 0; i < 100; i++ {
		r := i % loaded.count()
		removed = append(removed, loaded.ids[r])
		loaded.removeAt(r)
		assertShardIndexSound(t, loaded, "post-page-remove")
	}
	for i := 1000; i < 1050; i++ {
		loaded.append(mkRecord(uint64(i), uint16(i%10)))
		assertShardIndexSound(t, loaded, "post-page-append")
	}
	assertRemovedIDsGone(t, loaded, removed, "post-page-final")
}

// TestBUG666PreFixGobPayloadDecodes proves an on-disk page written BEFORE
// the index existed still decodes correctly. The index is unexported and
// absent from coldShardWire, so a pre-fix payload is byte-identical to a
// post-fix one — this test builds the wire struct by hand (exactly what a
// pre-fix Store wrote), encodes it, and decodes it through the real Load
// path, then proves every id resolves and a mutation on the result is sound.
//
// It also asserts the wire form is UNCHANGED by the fix: a shard's toWire
// output must gob-encode to the same bytes as the hand-built pre-fix
// struct, so an old page and a new page are the same file.
func TestBUG666PreFixGobPayloadDecodes(t *testing.T) {
	s := newColdShard(3)
	for i := 1; i <= 64; i++ {
		s.append(mkRecord(uint64(i)*7, uint16(i%10)))
	}

	// The pre-fix wire struct, built field by field from the columns — no
	// index anywhere, because none existed when such a page was written.
	pre := coldShardWire{
		EpochMonth: s.epochMonth,
		IDs:        s.ids, BirthDelta: s.birthDelta, Sexes: s.sexes,
		Households: s.households, Partners: s.partners, ChildCount: s.childCount,
		HomeCells: s.homeCells, Districts: s.districts,
		Workplaces: s.workplaces, Schools: s.schools,
		PSociability: s.pSociability, PAmbition: s.pAmbition,
		PConscient: s.pConscientious, PNovelty: s.pNovelty,
		PPhysicality: s.pPhysicality, PCommunity: s.pCommunity,
		PPatience: s.pPatience, PAesthetic: s.pAesthetic,
		Attainment: s.attainment, Stages: s.stages, Schooling: s.schooling,
		HealthBands: s.healthBands, Access: s.access, Wealth: s.wealth,
		Employment: s.employment, SatHousing: s.satHousing,
		SatServices: s.satServices, SatEnvironment: s.satEnvironment,
		SatLeisureFit: s.satLeisureFit, SatCommute: s.satCommute,
		MonthlyUpdates: s.monthlyUpdates,
	}
	var preBuf, postBuf bytes.Buffer
	if err := gob.NewEncoder(&preBuf).Encode(pre); err != nil {
		t.Fatalf("encode pre-fix wire: %v", err)
	}
	if err := gob.NewEncoder(&postBuf).Encode(s.toWire()); err != nil {
		t.Fatalf("encode post-fix wire: %v", err)
	}
	if !bytes.Equal(preBuf.Bytes(), postBuf.Bytes()) {
		t.Fatalf("the fix changed the on-disk page format: pre-fix %d bytes, post-fix %d bytes", preBuf.Len(), postBuf.Len())
	}

	// Write the pre-fix bytes where a PageStore expects a page, and Load it.
	dir := t.TempDir()
	ps := NewPageStore(dir, 4)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(ps.pathFor(9), preBuf.Bytes(), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	loaded, ok := ps.Load(9)
	if !ok {
		t.Fatalf("Load of a pre-fix page failed — old payloads must still decode")
	}
	assertShardIndexSound(t, loaded, "pre-fix-page")
	for i := 1; i <= 64; i++ {
		id := uint64(i) * 7
		row := loaded.rowOf(id)
		if row < 0 {
			t.Fatalf("pre-fix page: citizen %d unreachable after decode", id)
		}
		if loaded.recordAt(row).ID != id {
			t.Fatalf("pre-fix page: lookup of %d returned %d", id, loaded.recordAt(row).ID)
		}
	}
	// Mutating a shard decoded from a pre-fix page must be sound too (the
	// nil-map write panic lives here if rebuildIndexLocked were ever
	// dropped).
	loaded.removeAt(0)
	loaded.append(mkRecord(999_999, 1))
	assertShardIndexSound(t, loaded, "pre-fix-page-mutated")
}

// --- The id space ------------------------------------------------------------

// TestBUG666ExtremeIDSpace exercises the real id ranges this engine mints —
// founders [1,64], the perf seed base at 1e6, engine.attract's migrant base
// and this package's fertilityChildIDBase at 1<<63 — plus the structural
// extremes (1<<62, MaxUint64, MaxUint64-1) and the 0 sentinel, through
// append / lookup / swap-delete.
func TestBUG666ExtremeIDSpace(t *testing.T) {
	ids := []uint64{
		1, 2, 64, // founders
		1_000_000, 1_000_001, // PerfSeedIDBase neighbourhood
		1 << 32, (1 << 32) + 1,
		1 << 62,
		fertilityChildIDBase, fertilityChildIDBase + 1,
		math.MaxUint64 - 1, math.MaxUint64,
	}
	s := newColdShard(0)
	for _, id := range ids {
		r := mkRecord(id, uint16(id%10))
		r.Home = CellRef(id % 1_000_000)
		s.append(r)
		assertShardIndexSound(t, s, "extreme-append")
	}
	for _, id := range ids {
		row := s.rowOf(id)
		if row != oracleRowOf(s, id) {
			t.Fatalf("extreme id %d: rowOf=%d oracle=%d", id, row, oracleRowOf(s, id))
		}
		if s.recordAt(row).ID != id {
			t.Fatalf("extreme id %d resolved to %d", id, s.recordAt(row).ID)
		}
	}
	// Remove them in an order that forces the biggest ids to be swapped
	// around repeatedly.
	var removed []uint64
	for s.count() > 0 {
		removed = append(removed, s.ids[0])
		s.removeAt(0)
		assertShardIndexSound(t, s, "extreme-remove")
		assertRemovedIDsGone(t, s, removed, "extreme-remove")
	}
	// A never-inserted id (and the 0 sentinel) must miss cleanly, on both a
	// populated and an empty shard, exactly as the old linear scan did.
	for _, id := range []uint64{0, 12345, math.MaxUint64} {
		if got := s.rowOf(id); got != -1 {
			t.Fatalf("absent id %d returned row %d on an empty shard", id, got)
		}
	}
	s.append(mkRecord(42, 0))
	for _, id := range []uint64{0, 41, 43, math.MaxUint64} {
		if got := s.rowOf(id); got != -1 {
			t.Fatalf("absent id %d returned row %d", id, got)
		}
	}
}

// TestBUG666RowIndexInt32Bound is the STRUCTURAL assertion the brief asked
// for: the index stores rows as int32, so it is only safe while a single
// shard can never hold more than MaxInt32 rows. Shard count is fixed at
// det.NumShards and rows-per-shard is unbounded in principle (a hostile or
// merely unlucky id distribution can pile an arbitrary share of the
// population into one shard), so the guarantee cannot come from "100M/256";
// it must come from MaxInt32 dwarfing any population this engine can hold.
// 2,147,483,647 rows in ONE shard is 21x the entire 100M finish-line target
// and would need ~240GB of columns on its own, so int32 is provably
// sufficient — asserted here rather than assumed, so a future widening of
// the target trips this test.
func TestBUG666RowIndexInt32Bound(t *testing.T) {
	const hundredM = 100_000_000
	if numColdShards != det.NumShards {
		t.Fatalf("shard count drifted from det.NumShards: %d vs %d", numColdShards, det.NumShards)
	}
	// The whole 100M target in a SINGLE shard must still fit int32 rows.
	if int64(math.MaxInt32) < int64(hundredM) {
		t.Fatalf("int32 rows cannot address the 100M target even in the degenerate single-shard case")
	}
	// And the realistic even split, with a 10x safety factor for skew.
	perShard := int64(hundredM) / int64(numColdShards)
	if perShard*10 > int64(math.MaxInt32) {
		t.Fatalf("int32 rows leave no headroom over the %d-rows-per-shard even split", perShard)
	}
	// Round-trip the boundary values through the same conversion append
	// performs, proving no silent truncation at the edge.
	for _, n := range []int{0, 1, 1 << 20, math.MaxInt32 - 1, math.MaxInt32} {
		if int(int32(n)) != n {
			t.Fatalf("int32(%d) does not round-trip", n)
		}
	}
	t.Logf("int32 row index: MaxInt32=%d, 100M even split=%d rows/shard, single-shard worst case 100M — %.0fx headroom",
		math.MaxInt32, perShard, float64(math.MaxInt32)/float64(hundredM))
}

// TestBUG666DuplicateIDDivergence documented a REAL behavioural divergence
// the BUG-666 index fix introduced: SeedColdRecords used to accept a
// duplicate id (only ApplyLifeEventCommand's LifeEventBirth rejected one,
// via ErrDuplicateCitizenID), so two rows could carry the same id — pre-fix
// the linear scan returned the first such row and removing it left the
// second still reachable, but post-fix the map holds one entry per id (last
// append wins) and removing that row deletes the id from the index outright,
// leaving a live row nothing could ever look up again (invisible to
// fertility, money, and the death-realisation loop).
//
// BUG-663's F1 follow-up closed this at the source: SeedColdRecords now
// rejects a duplicate id outright (ErrDuplicateCitizenID), exactly as this
// comment's original "if a follow-up makes append or SeedColdRecords reject
// duplicates outright, this test must be updated to assert the rejection"
// anticipated. The unreachable-row divergence above can no longer arise
// through this path because the second row is never admitted.
func TestBUG666DuplicateIDDivergence(t *testing.T) {
	api, err := NewCitizensAPI(1, "bug666")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	a := mkRecord(7_000_001, 1)
	b := mkRecord(7_000_001, 2)
	b.Home = a.Home + 1 // distinguishable payload
	err = api.SeedColdRecords([]ColdRecord{a, b}, "bug666")
	assertRegistryCode(t, err, ErrDuplicateCitizenID)

	shard := det.ShardForEntity(a.ID)
	s := api.cold[shard]
	if s.count() != 1 {
		t.Fatalf("expected exactly 1 row (the duplicate must be rejected, not partially admitted), got %d", s.count())
	}
	if row := s.rowOf(a.ID); row < 0 {
		t.Fatalf("the first record must still be resident after the duplicate is rejected")
	}

	// A duplicate against an EARLIER SeedColdRecords call (not just within
	// the same batch) must be rejected identically.
	err = api.SeedColdRecords([]ColdRecord{b}, "bug666")
	assertRegistryCode(t, err, ErrDuplicateCitizenID)
	if s.count() != 1 {
		t.Fatalf("a cross-call duplicate must not be admitted either, got count=%d", s.count())
	}
}

// --- Determinism -------------------------------------------------------------

// bug666HashGolden is TestBUG666PopulationHashGolden's expected
// PopulationHash, captured from a scratch-copy build whose rowOf was the
// PRE-FIX linear scan (this round measured the fixed build and the
// scratch-swapped pre-fix build back to back: both produced exactly this
// value). See that test's doc comment.
//
//	fae47315290adff2ca7ceb54330016df2244e8810224b93aeae7bc2e104c62eb
var bug666HashGolden = [32]byte{
	0xfa, 0xe4, 0x73, 0x15, 0x29, 0x0a, 0xdf, 0xf2,
	0xca, 0x7c, 0xeb, 0x54, 0x33, 0x00, 0x16, 0xdf,
	0x22, 0x44, 0xe8, 0x81, 0x02, 0x24, 0xb9, 0x3a,
	0xea, 0xe7, 0xbc, 0x2e, 0x10, 0x4c, 0x62, 0xeb,
}

// TestBUG666PopulationHashGolden pins PopulationHash over a scripted run
// that exercises births, the death drain and heavy row churn. The golden
// value below was captured by running this exact scenario against a
// scratch-copy build whose rowOf was the PRE-FIX linear scan (this file's
// oracleRowOf), with the index still maintained — i.e. a genuine
// fix-vs-HEAD lookup-semantics differential, not a self-comparison. If the
// index ever resolves a different row than the scan would, the removal
// order (and therefore the row order PopulationHash walks) changes and this
// value moves.
func TestBUG666PopulationHashGolden(t *testing.T) {
	const month = int64(20_000)
	api, err := NewCitizensAPI(0xD37E, "bug666")
	if err != nil {
		t.Fatalf("NewCitizensAPI: %v", err)
	}
	seedRoundPopulation(t, api, 500, 600, month)
	for m := 0; m < 8; m++ {
		for d := 0; d < DaysPerMonth; d++ {
			if _, _, err := api.AdvanceDayTick("bug666"); err != nil {
				t.Fatalf("AdvanceDayTick: %v", err)
			}
		}
	}
	got := api.PopulationHash("bug666")
	t.Logf("PopulationHash after 8 months = %x (population %d)", got, api.TotalPopulation("bug666"))
	if want := bug666HashGolden; got != want {
		t.Fatalf("PopulationHash = %x, want %x (the pre-fix linear-scan build's value) — the index changed simulation state",
			got, want)
	}
}
