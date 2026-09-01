package crime

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-1972079943 — engine.crime implements the save.Participant contract
// (edge engine.crime→int.serializer), mirroring the engine.finance pilot and
// the engine.refuse / engine.citizens examples exactly. It is the 8th engine
// module to save its state through the per-module serialization pattern,
// completing the composed save.
//
// Serialization here is DATA-ONLY, like every prior participant.
// engine.crime DOES import foundation/det, but its RNG is STATELESS: every
// stochastic draw builds a fresh det.NewStream(seed, id, month, purpose) via
// the package-level detStream / offenderStream helpers, draws, and discards
// it immediately (gang territory, threat-event, and the justice
// charge/trial/sentence draws — verified against gangs.go, threat.go and
// justice.go). There is NO long-lived *det.Stream field on CrimeAPI and no
// mutable RNG cursor anywhere, so there is nothing RNG-shaped to persist. The
// reproducible-future inputs are (worldSeed, month): worldSeed is the
// construction/bundle-header input (a load target is a FRESH New(worldSeed,
// ...)), and month is carried per-call into AdvanceMonth.
//
// # Durable-vs-derived analysis (verified field-by-field against CrimeAPI,
// districtState, justiceState, Gang and threatState in api.go / types.go)
//
//	DURABLE — serialized:
//	  CrimeAPI scalars (one "crime.meta" record):
//	    - nextGangID: the gang-id counter. A respawned gang after a load must
//	      still mint a FRESH unique id (AC-9), so the counter must not rewind.
//	    - constabularyHQBuilt: the command-ladder gate (AC-10) — durable
//	      facility state, not recomputed.
//	    - mix (StrategyMix): the player-set citywide patrol/detective/community
//	      weighting (AC-10). Set once and retained; durable.
//	    - threat (threatState): the whole MI5-analogue dial — level,
//	      elevatedMonths (the rising-precursor streak), lastRiseMonth, and
//	      lastEventMonth. Every field accumulates ACROSS months and is read by
//	      the threat accessors; a dropped field would reset the precursor
//	      lead-window state (AC-11).
//	    - security (SecurityInput): the last pushed citywide security input.
//	      Retained on the struct and read by TriggerProbability() and
//	      advanceThreatLocked between ticks — a queryable surface that must
//	      round-trip so TriggerProbability() reproduces exactly after a load
//	      taken between ticks. (Re-supplied on the next AdvanceMonth, but a
//	      between-ticks query before that would otherwise read zero.)
//	  districts (one "crime.district" record per entry, SORTED by DistrictID
//	  NUMERICALLY — GR#21; DistrictID is uint64, a lexical sort would
//	  misorder 2 vs 10):
//	    - active[9]: the persistent active-crime STOCK per type — st.active[t]
//	      = persisted + gen carries into next month's persistence term. The
//	      single highest-value durable district field; read by ActiveCrime /
//	      SafetyTerm / cityCrimeActivityLocked.
//	    - generation[9] / rawGen[9] / persisted[9]: the current-month flow
//	      figures, each read by a public accessor (Generation / Recurrence /
//	      drill-through). They cannot be recomputed on load without re-running
//	      the month's stochastic draws, so a between-ticks save must carry
//	      them to reproduce the accessors' answers.
//	    - deterrence / clearance / prevention / effectiveClearance: the
//	      current-month policing-triad figures, each drill-through-queryable.
//	    - sustainedMonths: the consecutive-months gang-formation counter
//	      (AC-6) — accumulates across months; dropping it would reset an
//	      in-progress formation run.
//	    - eligiblePool: the remaining eligible pool (AC-7d), read by
//	      EligiblePool(); reduced in-month by recruitment.
//	    - recruitedCumulative: the persistent recruitment history that
//	      eligiblePool's monthly recompute discounts by (AC-7d) — durable
//	      across gang formation/removal cycles.
//	    - justice (justiceState, nested by value): backlog (the awaiting-trial
//	      STOCK carried across months — feeds effectiveClearanceLocked's
//	      backlog pressure) plus the nine current-month stage logs (arrested,
//	      charged, releasedNoCharge, convicted, acquitted, awaitingTrial,
//	      sentencedToPrison, sentencedNonCustodial, releasedOnBacklog), each
//	      read by an Offenders* accessor and VerifyPrisonIntake.
//	  gangs (one "crime.gang" record per entry, SORTED by GangID NUMERICALLY —
//	  GR#21): the full Gang entity by value — ID, Name, District, FormedAt,
//	  Strength, Territory, TaxLevyMicroPounds, BusinessClosures, Recruited.
//
//	DERIVED / CONFIG / INJECTED / RUNTIME — NOT serialized (excluded, with the
//	reason recorded in the field-parity drift test's allowlist):
//	    - seed: the worldSeed construction/header input (a load target is
//	      constructed with the same seed); not participant state.
//	    - cfg (config): immutable data (crime.json), reloaded by New — a save
//	      must not pin old balance rules (FEAT-1972079897).
//	    - prison (PrisonIntake): an INJECTED dependency (an interface wired by
//	      the composition root via SetPrisonIntake), re-wired on load — never
//	      part of a save.
//	    - correlationID: per-instance error correlation, not simulation state.
//	    - mu: runtime lock, not state.
//	    - self: SEC-020 copy-guard pointer, re-armed by New.
//	    - districtState.inputs: the last pushed DistrictInput is retained on
//	      the district struct but read by NOTHING (write-only; re-supplied on
//	      every AdvanceMonth from the composition root). Excluding it loses no
//	      observable behaviour; serializing a write-only re-supplied input
//	      cache is exactly the derived-input class the pattern excludes.
//
// Every wire projection carries explicit json tags: the domain structs
// (districtState / justiceState / Gang / threatState / StrategyMix /
// SecurityInput) are never marshalled directly (the field-parity drift tests
// guard this).
//
// SaveParticipant does NOT import internal/engine/save: it satisfies
// save.Participant STRUCTURALLY (Kind/Source/Handler), consuming only
// internal/foundation/serialize's Record/RecordSource/RecordHandler
// vocabulary — keeping this package on its single registered
// engine.crime→int.serializer edge.

const (
	// KindCrime is this participant's stable shard label. Must be unique
	// across a participant list; save.Load matches it against the shard
	// header's Kind to route the shard back here.
	KindCrime = "crime"

	recCrimeMeta     = "crime.meta"
	recCrimeDistrict = "crime.district"
	recCrimeGang     = "crime.gang"
)

// strategyMixWire is StrategyMix on the wire (the domain struct is never
// marshalled directly — the field-parity drift test guards this).
type strategyMixWire struct {
	Patrol    float64 `json:"patrol"`
	Detective float64 `json:"detective"`
	Community float64 `json:"community"`
}

// securityWire is SecurityInput on the wire.
type securityWire struct {
	Exposure float64 `json:"exposure"`
	Funding  float64 `json:"funding"`
	Liaison  float64 `json:"liaison"`
}

// threatStateWire is the whole threatState dial on the wire.
type threatStateWire struct {
	Level          float64 `json:"level"`
	ElevatedMonths int     `json:"elevatedMonths"`
	LastRiseMonth  int64   `json:"lastRiseMonth"`
	LastEventMonth int64   `json:"lastEventMonth"`
}

// crimeMetaWire carries the CrimeAPI's scalar/aggregate runtime state — every
// mutable field that is not one of the map-backed collections.
type crimeMetaWire struct {
	NextGangID          GangID          `json:"nextGangID"`
	ConstabularyHQBuilt bool            `json:"constabularyHQBuilt"`
	Mix                 strategyMixWire `json:"mix"`
	Threat              threatStateWire `json:"threat"`
	Security            securityWire    `json:"security"`
}

// justiceStateWire is one district's justiceState on the wire: the
// awaiting-trial backlog STOCK plus the nine current-month stage logs. Every
// slice is carried by value (defensively copied under the lock) so the wire
// never aliases a live justiceState slice.
type justiceStateWire struct {
	Backlog               []uint64 `json:"backlog"`
	Arrested              []uint64 `json:"arrested"`
	Charged               []uint64 `json:"charged"`
	ReleasedNoCharge      []uint64 `json:"releasedNoCharge"`
	Convicted             []uint64 `json:"convicted"`
	Acquitted             []uint64 `json:"acquitted"`
	AwaitingTrial         []uint64 `json:"awaitingTrial"`
	SentencedToPrison     []uint64 `json:"sentencedToPrison"`
	SentencedNonCustodial []uint64 `json:"sentencedNonCustodial"`
	ReleasedOnBacklog     []uint64 `json:"releasedOnBacklog"`
}

// crimeDistrictWire is one districts entry on the wire: the district ID (the
// flattened map key) plus the district's full mutable state, nested
// justiceState carried by value. The last-pushed `inputs` DistrictInput is
// intentionally absent — see the field-parity drift test's districtState
// allowlist.
type crimeDistrictWire struct {
	ID                  DistrictID             `json:"id"`
	Generation          [numCrimeTypes]float64 `json:"generation"`
	RawGen              [numCrimeTypes]float64 `json:"rawGen"`
	Persisted           [numCrimeTypes]float64 `json:"persisted"`
	Active              [numCrimeTypes]float64 `json:"active"`
	Deterrence          float64                `json:"deterrence"`
	Clearance           float64                `json:"clearance"`
	Prevention          float64                `json:"prevention"`
	EffectiveClearance  float64                `json:"effectiveClearance"`
	SustainedMonths     int                    `json:"sustainedMonths"`
	EligiblePool        int64                  `json:"eligiblePool"`
	RecruitedCumulative int64                  `json:"recruitedCumulative"`
	Justice             justiceStateWire       `json:"justice"`
}

// crimeGangWire is one gangs entry on the wire: the full Gang entity. The map
// key is Gang.ID, also carried in the record so a load reconstructs the map
// from the record alone.
type crimeGangWire struct {
	ID                 GangID     `json:"id"`
	Name               string     `json:"name"`
	District           DistrictID `json:"district"`
	FormedAt           int64      `json:"formedAt"`
	Strength           float64    `json:"strength"`
	Territory          []uint64   `json:"territory"`
	TaxLevyMicroPounds int64      `json:"taxLevyMicroPounds"`
	BusinessClosures   int64      `json:"businessClosures"`
	Recruited          int64      `json:"recruited"`
}

// cloneU64 returns a defensive copy of s (nil stays nil). Used so a wire
// projection never aliases a live justiceState / Gang slice.
func cloneU64(s []uint64) []uint64 {
	if s == nil {
		return nil
	}
	return append([]uint64(nil), s...)
}

// justiceToWire projects a justiceState to its wire form, copying every slice.
func justiceToWire(j justiceState) justiceStateWire {
	return justiceStateWire{
		Backlog:               cloneU64(j.backlog),
		Arrested:              cloneU64(j.arrested),
		Charged:               cloneU64(j.charged),
		ReleasedNoCharge:      cloneU64(j.releasedNoCharge),
		Convicted:             cloneU64(j.convicted),
		Acquitted:             cloneU64(j.acquitted),
		AwaitingTrial:         cloneU64(j.awaitingTrial),
		SentencedToPrison:     cloneU64(j.sentencedToPrison),
		SentencedNonCustodial: cloneU64(j.sentencedNonCustodial),
		ReleasedOnBacklog:     cloneU64(j.releasedOnBacklog),
	}
}

// justiceFromWire reconstructs a justiceState from its wire form, copying
// every slice so the loaded state never aliases the decoded wire.
func justiceFromWire(w justiceStateWire) justiceState {
	return justiceState{
		backlog:               cloneU64(w.Backlog),
		arrested:              cloneU64(w.Arrested),
		charged:               cloneU64(w.Charged),
		releasedNoCharge:      cloneU64(w.ReleasedNoCharge),
		convicted:             cloneU64(w.Convicted),
		acquitted:             cloneU64(w.Acquitted),
		awaitingTrial:         cloneU64(w.AwaitingTrial),
		sentencedToPrison:     cloneU64(w.SentencedToPrison),
		sentencedNonCustodial: cloneU64(w.SentencedNonCustodial),
		releasedOnBacklog:     cloneU64(w.ReleasedOnBacklog),
	}
}

// districtToWire projects a districtState to its wire form. The last-pushed
// `inputs` DistrictInput is intentionally NOT carried (write-only, re-supplied
// each AdvanceMonth — see the doc comment / drift-test allowlist).
func districtToWire(st *districtState) crimeDistrictWire {
	return crimeDistrictWire{
		ID:                  st.id,
		Generation:          st.generation,
		RawGen:              st.rawGen,
		Persisted:           st.persisted,
		Active:              st.active,
		Deterrence:          st.deterrence,
		Clearance:           st.clearance,
		Prevention:          st.prevention,
		EffectiveClearance:  st.effectiveClearance,
		SustainedMonths:     st.sustainedMonths,
		EligiblePool:        st.eligiblePool,
		RecruitedCumulative: st.recruitedCumulative,
		Justice:             justiceToWire(st.justice),
	}
}

// districtFromWire reconstructs a districtState from its wire form. `inputs` is
// left zero (excluded from the save; re-supplied on the next AdvanceMonth).
func districtFromWire(w crimeDistrictWire) *districtState {
	return &districtState{
		id:                  w.ID,
		generation:          w.Generation,
		rawGen:              w.RawGen,
		persisted:           w.Persisted,
		active:              w.Active,
		deterrence:          w.Deterrence,
		clearance:           w.Clearance,
		prevention:          w.Prevention,
		effectiveClearance:  w.EffectiveClearance,
		sustainedMonths:     w.SustainedMonths,
		eligiblePool:        w.EligiblePool,
		recruitedCumulative: w.RecruitedCumulative,
		justice:             justiceFromWire(w.Justice),
	}
}

// gangToWire projects a Gang to its wire form, copying the Territory slice.
func gangToWire(g *Gang) crimeGangWire {
	return crimeGangWire{
		ID:                 g.ID,
		Name:               g.Name,
		District:           g.District,
		FormedAt:           g.FormedAt,
		Strength:           g.Strength,
		Territory:          cloneU64(g.Territory),
		TaxLevyMicroPounds: g.TaxLevyMicroPounds,
		BusinessClosures:   g.BusinessClosures,
		Recruited:          g.Recruited,
	}
}

// gangFromWire reconstructs a Gang from its wire form, copying the Territory
// slice so the loaded gang never aliases the decoded wire.
func gangFromWire(w crimeGangWire) *Gang {
	return &Gang{
		ID:                 w.ID,
		Name:               w.Name,
		District:           w.District,
		FormedAt:           w.FormedAt,
		Strength:           w.Strength,
		Territory:          cloneU64(w.Territory),
		TaxLevyMicroPounds: w.TaxLevyMicroPounds,
		BusinessClosures:   w.BusinessClosures,
		Recruited:          w.Recruited,
	}
}

// crimeSnapshot is a point-in-time, deterministically-ordered copy of the
// mutable state, taken under the lock in one shot. The two map-backed
// collections are flattened to slices SORTED by NUMERIC key (GR#21 —
// DistrictID / GangID are uint64, so a lexical sort of their stringified form
// would misorder e.g. 2 before 10). The emitted record order — and therefore
// the saved bytes — is deterministic.
type crimeSnapshot struct {
	meta      crimeMetaWire
	districts []crimeDistrictWire // sorted by DistrictID, numerically
	gangs     []crimeGangWire     // sorted by GangID, numerically
}

// total is the number of records the snapshot emits: one meta record plus one
// per district and per gang.
func (s *crimeSnapshot) total() int {
	return 1 + len(s.districts) + len(s.gangs)
}

// recordAt marshals exactly the i-th record of the deterministic emission
// sequence (meta, districts, gangs) — one record's bytes, on demand.
func (s *crimeSnapshot) recordAt(i int) (serialize.Record, error) {
	kind, value := s.locate(i)
	data, err := json.Marshal(value)
	if err != nil {
		return serialize.Record{}, fmt.Errorf("crime: marshalling save record %d (kind %q): %w", i, kind, err)
	}
	return serialize.Record{Kind: kind, Data: data}, nil
}

// locate maps a global record index to its (Kind, wire value) without encoding
// anything — the pure index arithmetic behind recordAt.
func (s *crimeSnapshot) locate(i int) (string, any) {
	if i == 0 {
		return recCrimeMeta, s.meta
	}
	i--
	if i < len(s.districts) {
		return recCrimeDistrict, s.districts[i]
	}
	i -= len(s.districts)
	return recCrimeGang, s.gangs[i]
}

// snapshotForSave copies the full mutable state into a
// deterministically-ordered crimeSnapshot under the read lock. It reads
// everything in one locked pass so the snapshot is internally consistent, then
// releases the lock — Source encodes from the snapshot, not the live state.
func (a *CrimeAPI) snapshotForSave() (crimeSnapshot, error) {
	if err := a.checkNotCopied("snapshotForSave"); err != nil {
		return crimeSnapshot{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	snap := crimeSnapshot{
		meta: crimeMetaWire{
			NextGangID:          a.nextGangID,
			ConstabularyHQBuilt: a.constabularyHQBuilt,
			Mix: strategyMixWire{
				Patrol:    a.mix.Patrol,
				Detective: a.mix.Detective,
				Community: a.mix.Community,
			},
			Threat: threatStateWire{
				Level:          a.threat.level,
				ElevatedMonths: a.threat.elevatedMonths,
				LastRiseMonth:  a.threat.lastRiseMonth,
				LastEventMonth: a.threat.lastEventMonth,
			},
			Security: securityWire{
				Exposure: a.security.Exposure,
				Funding:  a.security.Funding,
				Liaison:  a.security.Liaison,
			},
		},
	}

	// Districts — sorted by DistrictID, NUMERICALLY (GR#21).
	districtIDs := make([]DistrictID, 0, len(a.districts))
	for id := range a.districts {
		districtIDs = append(districtIDs, id)
	}
	sort.Slice(districtIDs, func(i, j int) bool { return districtIDs[i] < districtIDs[j] })
	snap.districts = make([]crimeDistrictWire, 0, len(districtIDs))
	for _, id := range districtIDs {
		snap.districts = append(snap.districts, districtToWire(a.districts[id]))
	}

	// Gangs — sorted by GangID, NUMERICALLY (GR#21).
	gangIDs := make([]GangID, 0, len(a.gangs))
	for id := range a.gangs {
		gangIDs = append(gangIDs, id)
	}
	sort.Slice(gangIDs, func(i, j int) bool { return gangIDs[i] < gangIDs[j] })
	snap.gangs = make([]crimeGangWire, 0, len(gangIDs))
	for _, id := range gangIDs {
		snap.gangs = append(snap.gangs, gangToWire(a.gangs[id]))
	}

	return snap, nil
}

// resetForLoad clears the mutable state to empty under the write lock, before a
// Load streams records in. A load must REPLACE the state with the saved one, so
// every serialized runtime scalar/aggregate/map is reset here — the meta
// record (emitted FIRST) then overwrites the scalars/aggregates, and each
// district/gang record rebuilds the maps. The immutable config (cfg), the
// worldSeed (seed), the injected prison dependency, and the correlationID are
// left untouched: they are re-established by the composition root / New, not
// part of a save. nextGangID is reset to New's base (1) as a floor; the meta
// record overwrites it with the saved counter.
func (a *CrimeAPI) resetForLoad() error {
	if err := a.checkNotCopied("resetForLoad"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.districts = make(map[DistrictID]*districtState)
	a.gangs = make(map[GangID]*Gang)
	a.nextGangID = 1
	a.constabularyHQBuilt = false
	a.mix = StrategyMix{}
	a.threat = threatState{}
	a.security = SecurityInput{}
	return nil
}

// applyLoadRecord decodes one streamed record and installs its effect directly
// into the state under the write lock — one record at a time, the mirror of
// Source's one-record-at-a-time emission. Returns a decode/kind error verbatim
// so ReadShard fails loud and closed rather than loading a partial state
// silently.
func (a *CrimeAPI) applyLoadRecord(rec serialize.Record) error {
	if err := a.checkNotCopied("applyLoadRecord"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	switch rec.Kind {
	case recCrimeMeta:
		var m crimeMetaWire
		if err := json.Unmarshal(rec.Data, &m); err != nil {
			return fmt.Errorf("crime: decoding %s record: %w", rec.Kind, err)
		}
		a.nextGangID = m.NextGangID
		a.constabularyHQBuilt = m.ConstabularyHQBuilt
		a.mix = StrategyMix{Patrol: m.Mix.Patrol, Detective: m.Mix.Detective, Community: m.Mix.Community}
		a.threat = threatState{
			level:          m.Threat.Level,
			elevatedMonths: m.Threat.ElevatedMonths,
			lastRiseMonth:  m.Threat.LastRiseMonth,
			lastEventMonth: m.Threat.LastEventMonth,
		}
		a.security = SecurityInput{Exposure: m.Security.Exposure, Funding: m.Security.Funding, Liaison: m.Security.Liaison}

	case recCrimeDistrict:
		var w crimeDistrictWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("crime: decoding %s record: %w", rec.Kind, err)
		}
		a.districts[w.ID] = districtFromWire(w)

	case recCrimeGang:
		var w crimeGangWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("crime: decoding %s record: %w", rec.Kind, err)
		}
		a.gangs[w.ID] = gangFromWire(w)

	default:
		return fmt.Errorf("crime: unknown crime save record kind %q", rec.Kind)
	}
	return nil
}

// SaveParticipant adapts a *CrimeAPI to the save.Participant contract
// (Kind/Source/Handler) without this package importing engine/save — the
// interface is satisfied structurally. Construct via NewSaveParticipant; the
// wrapped CrimeAPI is the live state Source snapshots on save and the target
// Handler rebuilds on load.
type SaveParticipant struct {
	a *CrimeAPI
}

// NewSaveParticipant returns a SaveParticipant streaming/reconstructing a's
// state. On save it snapshots a; on load it resets a's runtime state and
// rebuilds it from the streamed records — so a load target is typically a
// FRESH New of the same worldSeed whose empty runtime state is replaced by the
// saved one (then re-wired by the composition root via SetPrisonIntake).
func NewSaveParticipant(a *CrimeAPI) *SaveParticipant {
	// SEC-020 pre-lock guard (astgate live-tree): a copied CrimeAPI is still
	// wrapped so the caller gets a non-nil participant, but every method below
	// re-checks checkNotCopied and fails closed, so a copy can never actually
	// read or mutate the state through this participant.
	_ = a.checkNotCopied("NewSaveParticipant")
	return &SaveParticipant{a: a}
}

// Kind returns the crime shard label. The SEC-020 guard mirrors every other
// method that reaches the wrapped candidate type (astgate live-tree): a copied
// CrimeAPI yields the empty kind, which save.Load and registry validation
// reject rather than routing a shard to a copy.
func (p *SaveParticipant) Kind() string {
	if err := p.a.checkNotCopied("Kind"); err != nil {
		return ""
	}
	return KindCrime
}

// Source returns a fresh pull-iterator over the crime state. It snapshots the
// full mutable state under the lock once, up front, then yields one record at a
// time, marshalling each on demand — never buffering the whole encoded shard
// before the first yield. The emission order is meta, then every district
// (sorted by numeric id), then every gang (sorted by numeric id). A
// copied-value guard failure (SEC-020) surfaces on the first pull.
func (p *SaveParticipant) Source() serialize.RecordSource {
	if err := p.a.checkNotCopied("Source"); err != nil {
		return func() (serialize.Record, bool, error) { return serialize.Record{}, false, err }
	}
	snap, snapErr := p.a.snapshotForSave()
	idx := 0
	return func() (serialize.Record, bool, error) {
		if snapErr != nil {
			err := snapErr
			snapErr = nil
			return serialize.Record{}, false, err
		}
		if idx >= snap.total() {
			return serialize.Record{}, false, nil
		}
		rec, err := snap.recordAt(idx)
		if err != nil {
			return serialize.Record{}, false, err
		}
		idx++
		return rec, true, nil
	}
}

// Handler returns a fresh sink that rebuilds the crime state from the streamed
// records. It clears the target's runtime state on the first record, then
// installs each record's effect directly under the lock — one record at a
// time, never buffering the whole shard.
func (p *SaveParticipant) Handler() serialize.RecordHandler {
	if err := p.a.checkNotCopied("Handler"); err != nil {
		return func(serialize.Record) error { return err }
	}
	reset := false
	return func(rec serialize.Record) error {
		if !reset {
			if err := p.a.resetForLoad(); err != nil {
				return err
			}
			reset = true
		}
		return p.a.applyLoadRecord(rec)
	}
}
