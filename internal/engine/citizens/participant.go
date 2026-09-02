package citizens

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-1972079941 inc9 — engine.citizens implements the save.Participant
// contract (edge engine.citizens→int.serializer), mirroring the inc1
// engine.finance pilot and the inc2..inc8 examples. It is the CAPSTONE of
// the per-module serialization epic: the population is the single most
// save-critical state in the engine (up to 100M persistent individuals,
// ~6.7GB of cold records), so this participant is the one that actually
// exercises int.serializer's streaming contract at scale.
//
// Serialization here is DATA-ONLY, like every prior inc. engine.citizens
// DOES import foundation/det, but its RNG is STATELESS: every draw builds a
// fresh det.NewStream(seed, id, month, purpose), draws, and discards it
// immediately (mortality/education/employment/fertility/personality —
// verified against coldpass.go / fertility.go / citizen.go). There is no
// *det.Stream field on CitizensAPI and no mutable RNG cursor anywhere, so
// there is nothing RNG-shaped to persist. The reproducible-future inputs
// are (worldSeed, month): worldSeed is the construction/bundle-header input
// (a load target is a FRESH NewCitizensAPI(worldSeed, ...)), and month is
// carried in the meta record below.
//
// # Durable-vs-derived analysis (the highest-value decision), verified
// field-by-field against CitizensAPI (registry.go), ColdShard/ColdRecord
// (coldshard.go), Household (household.go) and ColdPassParams (coldpass.go):
//
//   DURABLE — serialized:
//     - month, dayTick: the two-layer sim clock (drives age derivation and
//       the cold-pass month boundary). meta.
//     - cold [256]*ColdShard: the SINGLE SOURCE OF TRUTH for every citizen.
//       Streamed as one "citizens.cold" record per citizen (ColdRecord's 24
//       fields PLUS the per-row MonthlyUpdates column — see below).
//     - households + nextHouseholdID: the household entities and their id
//       counter. "citizens.household" records + meta.
//     - nextFertilityChildID: the fertility-child id counter (disjoint id
//       space, fertility.go's fertilityChildIDBase). meta.
//     - curMonthBirths/Deaths, lastMonthBirths/Deaths: the vital-event
//       accumulators (VitalEvents' conservation surface). meta.
//     - monthParams (ColdPassParams): the sample-derived cold-pass params
//       computed at the START of the current month and applied across its 30
//       day-ticks. This is genuine MID-MONTH state: AdvanceDayTick only
//       recomputes it when dayTick==0, and re-deriving it mid-month from the
//       (by-then mutated) population would give DIFFERENT params than the
//       ones the month actually started with. A mid-month save that dropped
//       it would silently reset the rest of the month to a fresh-API
//       ColdPassParams{MortalityMultiplier:1}. So it is serialized (meta),
//       not recomputed on load. (See the participant report's mid-month
//       finding.)
//     - ColdShard.epochMonth (per shard): the age-delta epoch (birthDelta[i]
//       + epochMonth == absolute birth month). In the live engine it is
//       invariantly 0 (only newColdShard(0) is ever constructed and nothing
//       re-epochs), and ColdRecord.BirthMonth is ABSOLUTE, so BirthMonth
//       round-trips losslessly even through an epoch-0 restore — but the
//       epoch is carried per shard in meta.EpochMonths anyway so a future
//       non-zero epoch would survive, and it is restored BEFORE any cold
//       record is appended.
//     - ColdShard.monthlyUpdates (per row): AC-7's exactly-once-per-month
//       guard AND a PopulationHash input. ColdRecord does NOT carry it (it
//       is shard bookkeeping, not a citizen field), so a ColdRecord-only
//       save would silently drop it and let a mid-month load double-advance
//       or skip a scheduled shard. It is carried alongside each cold record
//       as MonthlyUpdates.
//     - hot Fidelity tier (per elevated id): the ONLY genuine state on the
//       hot elevation cache. The rest of a hot record is DERIVED — it is
//       rebuilt by coldRecordToHot from the (already-restored) cold record,
//       exactly as ApplyFidelityCommand's COLD→HOT path does; PopulationHash
//       confirms this by hashing only Fidelity for hot ids, never the body.
//       "citizens.fidelity" records carry {ID, Fidelity} only.
//
//   DERIVED / CONFIG / RUNTIME — NOT serialized:
//     - seed: the worldSeed construction/header input (a load target is
//       constructed with the same seed); not participant state.
//     - workers: the POOL-SIM worker count (affects only wall-clock, never
//       results, AC-17); a perf knob, not state.
//     - fertilityCfg: immutable data/fertility.json config, reloaded by
//       NewCitizensAPI (a save must not pin old balance rules —
//       FEAT-1972079897).
//     - the hot record BODY (everything but Fidelity): rebuilt via
//       coldRecordToHot on load.
//     - mu: runtime lock, not state.
//     - self: SEC-020 copy-guard pointer, re-armed by NewCitizensAPI.
//
// # Streaming (the 100M-citizen contract — the whole point of this inc)
//
// int.serializer's RecordSource contract is "nothing holds more than one
// record's bytes at once", and save.Participant forbids buffering the whole
// record set before Source starts yielding. The cold store is up to ~6.7GB,
// so Source MUST NOT copy it wholesale (the existing read-all
// allColdRecordsLocked() is exactly what NOT to use here). Instead the cold
// records stream through coldStream, a lazy enumerator that snapshots ONE of
// the 256 shards at a time under a fresh read lock (a bounded per-shard
// collect — at 100M that is ~26MB for a single shard, never the whole
// population) and yields its rows before touching the next shard. The
// small, RESIDENT collections (meta, households, the hot Fidelity set) are
// snapshotted once up front under a single lock, exactly like the finance
// pilot — households and the hot cache are already fully memory-resident by
// the module's own design (only cold shards page to disk, paging.go), so
// flattening+sorting their keys is the same O(resident) cost HouseholdIDs()
// and PopulationHash already pay.
//
// Consistency: like every prior participant, this assumes the state is
// QUIESCENT for the duration of a save (saves are taken between ticks). The
// finance/world pilots get a whole-state point-in-time snapshot for free
// because their durable state is small enough to copy in one locked pass;
// the cold population cannot be copied wholesale, so its point-in-time
// consistency rests on that same between-ticks assumption rather than a
// single mega-snapshot — the lock is still taken per shard, never held
// across the serializer's I/O.
//
// SaveParticipant does NOT import internal/engine/save: it satisfies
// save.Participant STRUCTURALLY (Kind/Source/Handler), consuming only
// internal/foundation/serialize's Record/RecordSource/RecordHandler
// vocabulary — keeping this package on its single registered
// engine.citizens→int.serializer edge.

const (
	// KindCitizens is this participant's stable shard label. Must be unique
	// across a participant list; save.Load matches it against the shard
	// header's Kind to route the shard back here.
	KindCitizens = "citizens"

	recCitizensMeta      = "citizens.meta"
	recCitizensCold      = "citizens.cold"
	recCitizensHousehold = "citizens.household"
	recCitizensFidelity  = "citizens.fidelity"
)

// coldPassParamsWire is ColdPassParams's wire projection (the mid-month
// cold-pass parameter set). Explicit json tags; the domain struct is never
// marshalled directly (the field-parity drift test guards this).
type coldPassParamsWire struct {
	MortalityMultiplier     float64 `json:"mortalityMultiplier"`
	EducationTransitionRate float64 `json:"educationTransitionRate"`
	JobMatchRate            float64 `json:"jobMatchRate"`
	HealthDrift             int32   `json:"healthDrift"`
	SatisfactionDrift       int32   `json:"satisfactionDrift"`
	LowConfidence           bool    `json:"lowConfidence"`
}

// The two projections convert directly (identical field set) into the
// TAGGED wire type — the domain struct is still never json.Marshalled
// directly (AC-2), and a field added to ColdPassParams without a matching
// wire field breaks this conversion at COMPILE time (a stronger drift guard
// than the reflective test alone). Mirrors finance's entryWire(e) precedent.
func toColdPassParamsWire(p ColdPassParams) coldPassParamsWire { return coldPassParamsWire(p) }

func fromColdPassParamsWire(w coldPassParamsWire) ColdPassParams { return ColdPassParams(w) }

// citizensMetaWire carries the CitizensAPI's scalar/counter state plus the
// mid-month cold-pass params and the per-shard age-delta epochs. It is the
// single "citizens.meta" record, always emitted FIRST so the shard epochs
// are restored before any cold record is appended. EpochMonths has exactly
// numColdShards entries.
type citizensMetaWire struct {
	Month                int64              `json:"month"`
	DayTick              int                `json:"dayTick"`
	NextHouseholdID      uint64             `json:"nextHouseholdID"`
	NextFertilityChildID uint64             `json:"nextFertilityChildID"`
	CurMonthBirths       int                `json:"curMonthBirths"`
	CurMonthDeaths       int                `json:"curMonthDeaths"`
	LastMonthBirths      int                `json:"lastMonthBirths"`
	LastMonthDeaths      int                `json:"lastMonthDeaths"`
	MonthParams          coldPassParamsWire `json:"monthParams"`
	EpochMonths          []int64            `json:"epochMonths"`
}

// coldCitizenWire is one cold citizen on the wire: every ColdRecord field
// (json-tagged) PLUS the per-row MonthlyUpdates column (shard bookkeeping
// ColdRecord does not carry — see the durable analysis above). The field
// set and order mirror ColdRecord exactly, then append MonthlyUpdates; the
// field-parity drift test asserts coldCitizenWire == ColdRecord's fields + 1.
type coldCitizenWire struct {
	ID              uint64                   `json:"id"`
	BirthMonth      int64                    `json:"birthMonth"`
	Sex             Sex                      `json:"sex"`
	Household       uint64                   `json:"household"` // widened from uint32 — births-unblock lane, 2026-09-02; JSON numbers are untyped so an old uint32-range save still decodes correctly
	Partner         uint64                   `json:"partner"`   // widened from uint32 — births-unblock lane, 2026-09-02
	ChildCount      uint8                    `json:"childCount"`
	Home            CellRef                  `json:"home"`
	District        uint16                   `json:"district"`
	Workplace       uint32                   `json:"workplace"`
	School          uint32                   `json:"school"`
	Personality     [NumPersonalityAxes]int8 `json:"personality"`
	Attainment      int16                    `json:"attainment"`
	Stage           Stage                    `json:"stage"`
	Schooling       int16                    `json:"schooling"`
	HealthBand      HealthBand               `json:"healthBand"`
	Access          uint8                    `json:"access"`
	Wealth          int64                    `json:"wealth"`
	EmploymentState EmploymentState          `json:"employmentState"`
	Sector          Sector                   `json:"sector"`
	SatHousing      int32                    `json:"satHousing"`
	SatServices     int32                    `json:"satServices"`
	SatEnvironment  int32                    `json:"satEnvironment"`
	SatLeisureFit   int32                    `json:"satLeisureFit"`
	SatCommute      int32                    `json:"satCommute"`

	// MonthlyUpdates is the per-row exactly-once-per-month bookkeeping column
	// (ColdShard.monthlyUpdates) — NOT a ColdRecord field, carried here so a
	// mid-month save round-trips AC-7's guard and the PopulationHash input.
	MonthlyUpdates uint32 `json:"monthlyUpdates"`
}

func coldRecordToWire(r ColdRecord, monthlyUpdates uint32) coldCitizenWire {
	return coldCitizenWire{
		ID:              r.ID,
		BirthMonth:      r.BirthMonth,
		Sex:             r.Sex,
		Household:       r.Household,
		Partner:         r.Partner,
		ChildCount:      r.ChildCount,
		Home:            r.Home,
		District:        r.District,
		Workplace:       r.Workplace,
		School:          r.School,
		Personality:     r.Personality,
		Attainment:      r.Attainment,
		Stage:           r.Stage,
		Schooling:       r.Schooling,
		HealthBand:      r.HealthBand,
		Access:          r.Access,
		Wealth:          r.Wealth,
		EmploymentState: r.EmploymentState,
		Sector:          r.Sector,
		SatHousing:      r.SatHousing,
		SatServices:     r.SatServices,
		SatEnvironment:  r.SatEnvironment,
		SatLeisureFit:   r.SatLeisureFit,
		SatCommute:      r.SatCommute,
		MonthlyUpdates:  monthlyUpdates,
	}
}

func wireToColdRecord(w coldCitizenWire) ColdRecord {
	return ColdRecord{
		ID:              w.ID,
		BirthMonth:      w.BirthMonth,
		Sex:             w.Sex,
		Household:       w.Household,
		Partner:         w.Partner,
		ChildCount:      w.ChildCount,
		Home:            w.Home,
		District:        w.District,
		Workplace:       w.Workplace,
		School:          w.School,
		Personality:     w.Personality,
		Attainment:      w.Attainment,
		Stage:           w.Stage,
		Schooling:       w.Schooling,
		HealthBand:      w.HealthBand,
		Access:          w.Access,
		Wealth:          w.Wealth,
		EmploymentState: w.EmploymentState,
		Sector:          w.Sector,
		SatHousing:      w.SatHousing,
		SatServices:     w.SatServices,
		SatEnvironment:  w.SatEnvironment,
		SatLeisureFit:   w.SatLeisureFit,
		SatCommute:      w.SatCommute,
	}
}

// householdWire is one household on the wire.
type householdWire struct {
	ID            uint64   `json:"id"`
	DwellingRooms uint8    `json:"dwellingRooms"`
	Members       []uint64 `json:"members"`
}

// fidelityWire is one elevated (HOT/WARM) citizen's fidelity tier on the
// wire. The rest of the hot record is DERIVED and rebuilt from the cold
// record on load, so only the tier travels.
type fidelityWire struct {
	ID       uint64   `json:"id"`
	Fidelity Fidelity `json:"fidelity"`
}

// citizensHead is the small, RESIDENT part of the save (everything but the
// streamed cold population): the meta record plus the sorted household and
// fidelity slices. Snapshotted once under a single read lock.
type citizensHead struct {
	meta       citizensMetaWire
	households []householdWire // sorted by id (GR#21)
	fidelity   []fidelityWire  // sorted by id (GR#21)
}

// snapshotHead copies the meta scalars, the households, and the hot Fidelity
// set into a deterministically-ordered citizensHead under the read lock
// (GR#21: maps flattened to slices sorted by numeric uint64 key). The cold
// population is NOT captured here — it streams lazily through coldStream.
func (c *CitizensAPI) snapshotHead() (citizensHead, error) {
	if err := c.checkNotCopied(errs.NewCorrelationID(), "snapshotHead"); err != nil {
		return citizensHead{}, err
	}
	c.mu.RLock()
	defer c.mu.RUnlock()

	epochs := make([]int64, numColdShards)
	for i := range c.cold {
		epochs[i] = c.cold[i].epochMonth
	}

	head := citizensHead{
		meta: citizensMetaWire{
			Month:                c.month,
			DayTick:              c.dayTick,
			NextHouseholdID:      c.nextHouseholdID,
			NextFertilityChildID: c.nextFertilityChildID,
			CurMonthBirths:       c.curMonthBirths,
			CurMonthDeaths:       c.curMonthDeaths,
			LastMonthBirths:      c.lastMonthBirths,
			LastMonthDeaths:      c.lastMonthDeaths,
			MonthParams:          toColdPassParamsWire(c.monthParams),
			EpochMonths:          epochs,
		},
	}

	// Households — sorted by id, numerically (GR#21). Members copied so the
	// wire never aliases the live household slice.
	hhIDs := make([]uint64, 0, len(c.households))
	for id := range c.households {
		hhIDs = append(hhIDs, id)
	}
	sort.Slice(hhIDs, func(i, j int) bool { return hhIDs[i] < hhIDs[j] })
	head.households = make([]householdWire, 0, len(hhIDs))
	for _, id := range hhIDs {
		h := c.households[id]
		head.households = append(head.households, householdWire{
			ID:            h.ID,
			DwellingRooms: h.DwellingRooms,
			Members:       append([]uint64(nil), h.Members...),
		})
	}

	// Hot Fidelity set — sorted by id, numerically (GR#21). Only the tier.
	hotIDs := make([]uint64, 0, len(c.hot))
	for id := range c.hot {
		hotIDs = append(hotIDs, id)
	}
	sort.Slice(hotIDs, func(i, j int) bool { return hotIDs[i] < hotIDs[j] })
	head.fidelity = make([]fidelityWire, 0, len(hotIDs))
	for _, id := range hotIDs {
		head.fidelity = append(head.fidelity, fidelityWire{ID: id, Fidelity: c.hot[id].Fidelity})
	}

	return head, nil
}

// snapshotColdShard copies ONE cold shard's rows into a bounded slice of
// wire records under the read lock, then releases the lock. This is the
// per-shard collect coldStream walks the 256 shards through — bounded to a
// single shard (~26MB at 100M citizens), never the whole 6.7GB population.
// Rows are yielded in the shard's own index order, which is stable for a
// fixed population (removeAt is swap-with-last, but nothing mutates during a
// quiescent save), so the emitted order is deterministic.
func (c *CitizensAPI) snapshotColdShard(shard int) []coldCitizenWire {
	if err := c.checkNotCopied(errs.NewCorrelationID(), "snapshotColdShard"); err != nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	s := c.cold[shard]
	n := s.count()
	out := make([]coldCitizenWire, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, coldRecordToWire(s.recordAt(i), s.monthlyUpdates[i]))
	}
	return out
}

// coldStream is a lazy, shard-by-shard enumerator over the cold population.
// It snapshots ONE shard at a time (snapshotColdShard, which locks
// internally) and yields its rows before touching the next shard, so Source
// never materialises the whole cold store before its first yield. snapshots
// counts how many shards have been snapshotted so far — the test seam that
// proves laziness (a full-population snapshot would drive it straight to
// numColdShards on the first pull).
type coldStream struct {
	c         *CitizensAPI
	shard     int               // next shard index to snapshot
	buf       []coldCitizenWire // current shard's rows
	pos       int               // next index within buf
	snapshots int               // shards snapshotted so far (laziness test seam)
}

func (c *CitizensAPI) newColdStream() *coldStream {
	// SEC-020 pre-lock guard (astgate live-tree): a copied CitizensAPI still
	// yields a stream, but every next() re-checks and fails closed.
	_ = c.checkNotCopied(errs.NewCorrelationID(), "newColdStream")
	return &coldStream{c: c}
}

// next returns the next cold citizen wire record, advancing shard-by-shard
// and loading each shard lazily. It returns ok=false once all 256 shards are
// exhausted, or immediately (fail-closed) if the wrapped CitizensAPI is a
// struct copy (SEC-020).
func (cs *coldStream) next() (coldCitizenWire, bool) {
	if err := cs.c.checkNotCopied(errs.NewCorrelationID(), "coldStream.next"); err != nil {
		return coldCitizenWire{}, false
	}
	for cs.pos >= len(cs.buf) {
		if cs.shard >= numColdShards {
			return coldCitizenWire{}, false
		}
		cs.buf = cs.c.snapshotColdShard(cs.shard)
		cs.snapshots++
		cs.shard++
		cs.pos = 0
	}
	r := cs.buf[cs.pos]
	cs.pos++
	return r, true
}

// resetForLoad clears the mutable DURABLE state to an empty registry under
// the write lock, before a Load streams records in. A load must REPLACE the
// state with the saved one, so every serialized collection is reset here —
// the cold shards are re-created empty at epoch 0 (the meta record, emitted
// first, restores each shard's real epoch before any cold record appends),
// the hot cache and households are emptied, and every scalar is zeroed
// (meta overwrites them). fertilityCfg (config), seed/workers (config), mu
// and self (runtime/guard) are left untouched.
func (c *CitizensAPI) resetForLoad() error {
	if err := c.checkNotCopied(errs.NewCorrelationID(), "resetForLoad"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.cold {
		c.cold[i] = newColdShard(0)
	}
	c.hot = make(map[uint64]*Citizen)
	c.households = make(map[uint64]*Household)
	c.month = 0
	c.dayTick = 0
	c.nextHouseholdID = 1
	c.nextFertilityChildID = 0
	c.curMonthBirths = 0
	c.curMonthDeaths = 0
	c.lastMonthBirths = 0
	c.lastMonthDeaths = 0
	c.monthParams = ColdPassParams{MortalityMultiplier: 1.0}
	return nil
}

// applyLoadRecord decodes one streamed record and installs its effect
// directly into the registry under the write lock — one record at a time,
// never buffering the whole shard (the mirror of Source's streaming
// emission). A cold record is validated through the SAME ValidateColdRecord
// the seed path uses (GR#16: a load is an untrusted boundary), and a
// fidelity record referencing an unknown citizen is rejected loud-and-closed
// rather than installing a hot entry with no cold backing.
func (c *CitizensAPI) applyLoadRecord(rec serialize.Record) error {
	cid := errs.NewCorrelationID()
	if err := c.checkNotCopied(cid, "applyLoadRecord"); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	switch rec.Kind {
	case recCitizensMeta:
		var m citizensMetaWire
		if err := json.Unmarshal(rec.Data, &m); err != nil {
			return fmt.Errorf("citizens: decoding %s record: %w", rec.Kind, err)
		}
		if len(m.EpochMonths) != numColdShards {
			return fmt.Errorf("citizens: decoding %s record: epochMonths has %d entries, want %d", rec.Kind, len(m.EpochMonths), numColdShards)
		}
		c.month = m.Month
		c.dayTick = m.DayTick
		c.nextHouseholdID = m.NextHouseholdID
		c.nextFertilityChildID = m.NextFertilityChildID
		c.curMonthBirths = m.CurMonthBirths
		c.curMonthDeaths = m.CurMonthDeaths
		c.lastMonthBirths = m.LastMonthBirths
		c.lastMonthDeaths = m.LastMonthDeaths
		c.monthParams = fromColdPassParamsWire(m.MonthParams)
		// Restore each shard's age-delta epoch BEFORE any cold record appends
		// (append derives birthDelta relative to the shard's epoch).
		for i := range c.cold {
			c.cold[i].epochMonth = m.EpochMonths[i]
		}

	case recCitizensCold:
		var w coldCitizenWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("citizens: decoding %s record: %w", rec.Kind, err)
		}
		r := wireToColdRecord(w)
		// Validate through the same guard the seed path uses (GR#16/AC-13).
		if err := ValidateColdRecord(r, cid); err != nil {
			return err
		}
		shard := det.ShardForEntity(r.ID)
		if err := validateShardIndex(shard, numColdShards, cid); err != nil {
			return err
		}
		s := c.cold[shard]
		s.append(r)
		// Restore the per-row bookkeeping column append zeroed (the one
		// durable cold value ColdRecord does not carry).
		s.monthlyUpdates[s.count()-1] = w.MonthlyUpdates

	case recCitizensHousehold:
		var w householdWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("citizens: decoding %s record: %w", rec.Kind, err)
		}
		c.households[w.ID] = &Household{
			ID:            w.ID,
			Members:       append([]uint64(nil), w.Members...),
			DwellingRooms: w.DwellingRooms,
		}

	case recCitizensFidelity:
		var w fidelityWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("citizens: decoding %s record: %w", rec.Kind, err)
		}
		if w.Fidelity > FidelityHot {
			return fmt.Errorf("citizens: decoding %s record: citizen %d has out-of-range fidelity %d", rec.Kind, w.ID, w.Fidelity)
		}
		// The hot record body is DERIVED: rebuild it from the (already
		// restored) cold record, exactly as ApplyFidelityCommand's COLD→HOT
		// path does, then stamp the saved tier. A fidelity record with no
		// cold backing is corruption — fail closed rather than aliasing a hot
		// entry to a citizen that does not exist.
		r, ok := c.coldRecord(w.ID)
		if !ok {
			return fmt.Errorf("citizens: decoding %s record: elevated citizen %d has no cold record (corrupt save)", rec.Kind, w.ID)
		}
		cit := coldRecordToHot(r, c.month)
		cit.Fidelity = w.Fidelity
		c.hot[w.ID] = &cit

	default:
		return fmt.Errorf("citizens: unknown citizens save record kind %q", rec.Kind)
	}
	return nil
}

// SaveParticipant adapts a *CitizensAPI to the save.Participant contract
// (Kind/Source/Handler) without this package importing engine/save — the
// interface is satisfied structurally. Construct via NewSaveParticipant; the
// wrapped CitizensAPI is the live state Source streams on save and the
// target Handler rebuilds on load.
type SaveParticipant struct {
	c *CitizensAPI
}

// NewSaveParticipant returns a SaveParticipant streaming/reconstructing c's
// state. On save it streams c (meta, then the cold population shard-by-shard,
// then households, then the hot fidelity set); on load it resets c and
// rebuilds it from the streamed records — so a load target is typically a
// FRESH NewCitizensAPI(sameSeed, ...) whose empty registry is replaced by
// the saved population.
func NewSaveParticipant(c *CitizensAPI) *SaveParticipant {
	// SEC-020 pre-lock guard (astgate live-tree): a copied CitizensAPI is
	// still wrapped so the caller gets a non-nil participant, but every method
	// below re-checks checkNotCopied and fails closed, so a copy can never
	// actually read or mutate the state through this participant.
	_ = c.checkNotCopied(errs.NewCorrelationID(), "NewSaveParticipant")
	return &SaveParticipant{c: c}
}

// Kind returns the citizens shard label. The SEC-020 guard mirrors every
// other method that reaches the wrapped candidate type (astgate live-tree):
// a copied CitizensAPI yields the empty kind, which save.Load and registry
// validation reject rather than routing a shard to a copy.
func (p *SaveParticipant) Kind() string {
	if err := p.c.checkNotCopied(errs.NewCorrelationID(), "Kind"); err != nil {
		return ""
	}
	return KindCitizens
}

// Source returns a fresh pull-iterator over the citizen state. It snapshots
// the small resident head (meta + households + fidelity) under the lock once,
// up front, then streams the cold population shard-by-shard through coldStream
// — never buffering the whole 6.7GB cold store before the first yield. The
// emission order is meta, then every cold citizen (shard 0→255, row order),
// then households (sorted), then the hot fidelity set (sorted). A
// copied-value guard failure (SEC-020) surfaces on the first pull.
func (p *SaveParticipant) Source() serialize.RecordSource {
	if err := p.c.checkNotCopied(errs.NewCorrelationID(), "Source"); err != nil {
		return func() (serialize.Record, bool, error) { return serialize.Record{}, false, err }
	}
	head, headErr := p.c.snapshotHead()
	cold := p.c.newColdStream()
	metaEmitted := false
	hi, fi := 0, 0
	emit := func(kind string, value any) (serialize.Record, bool, error) {
		data, err := json.Marshal(value)
		if err != nil {
			return serialize.Record{}, false, fmt.Errorf("citizens: marshalling %s save record: %w", kind, err)
		}
		return serialize.Record{Kind: kind, Data: data}, true, nil
	}
	return func() (serialize.Record, bool, error) {
		if headErr != nil {
			err := headErr
			headErr = nil
			return serialize.Record{}, false, err
		}
		if !metaEmitted {
			metaEmitted = true
			return emit(recCitizensMeta, head.meta)
		}
		if w, ok := cold.next(); ok {
			return emit(recCitizensCold, w)
		}
		if hi < len(head.households) {
			w := head.households[hi]
			hi++
			return emit(recCitizensHousehold, w)
		}
		if fi < len(head.fidelity) {
			w := head.fidelity[fi]
			fi++
			return emit(recCitizensFidelity, w)
		}
		return serialize.Record{}, false, nil
	}
}

// Handler returns a fresh sink that rebuilds the citizen state from the
// streamed records. It clears the target registry on the first record, then
// installs each record's effect directly under the lock — one record at a
// time, never buffering the whole shard.
func (p *SaveParticipant) Handler() serialize.RecordHandler {
	if err := p.c.checkNotCopied(errs.NewCorrelationID(), "Handler"); err != nil {
		return func(serialize.Record) error { return err }
	}
	reset := false
	return func(rec serialize.Record) error {
		if !reset {
			if err := p.c.resetForLoad(); err != nil {
				return err
			}
			reset = true
		}
		return p.c.applyLoadRecord(rec)
	}
}
