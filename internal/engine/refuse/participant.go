package refuse

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-1972079941 inc5 — engine.refuse implements the save.Participant
// contract (edge engine.refuse→int.serializer), mirroring the inc1
// engine.finance pilot, the inc2 engine.unlocks example, the inc3
// engine.build example, and the inc4 engine.market example exactly. It is
// the FIFTH engine module to save its state through the per-module
// serialization pattern.
//
// Serialization here is DATA-ONLY, like finance/unlocks/build/market:
// engine.refuse has NO foundation/det RNG at all (no det import — verified
// by grep; the only sync.* reference in refuse.go is the RWMutex guarding
// the mutable state), so there is no mutable RNG cursor to persist. The
// reproducible-future inputs are (worldSeed, month) [in the save-bundle
// header]; a lossless save is exactly the module's mutable runtime state.
//
// The MUTABLE runtime state this participant persists — every other
// RefuseAPI field is either the runtime lock, the per-instance
// correlationID, the immutable config loaded from data/refuse.json (cfg —
// NOT serialized: the hard-reset-replay premise is that a save must not pin
// old rules, FEAT-1972079897), an injected dependency re-wired by the
// composition root on load (logistics/services/wellbeing), the SEC-020
// copy-guard pointer (self), or the `provisioned` logistics-shelf-coupling
// map (see its exclusion note in the field-parity drift test):
//
//   - scalars/arrays (a single "refuse.meta" record): generated[3] and
//     collected[3] (the per-stream cumulative flow counters, in streamOrder
//     layout — they MUST round-trip so the mass-conservation identity and
//     the recycling-resale value survive a load), contamination (city-wide
//     recycling contamination [0,1]), generalSiteID / compostSiteID (the
//     active general-waste / food-waste disposal targets), and
//     trucksAvailable (the refuse-crew-derived truck count last set);
//   - one "refuse.cell" record per cells entry (SORTED by cell ID, GR#21):
//     the cell's full mutable bin state — land use, street, capacity, the
//     three per-stream in-bin levels and overflow amounts, the accumulated
//     vermin index, and the last miss cause (a *MissCause, carried by value
//     with a defensive copy so the wire never aliases the live field);
//   - one "refuse.round" record per rounds entry (SORTED by round ID,
//     GR#21): the round's depot, cell set, effective route, override state
//     and override route, the completed flag, and the per-stream in-transit
//     tonnage collected-but-not-yet-delivered (a lost in-transit load is
//     exactly the mass-conservation corruption this guards against). The
//     transient `active` re-entrancy flag is deliberately NOT persisted —
//     see the field-parity drift test's roundState allowlist;
//   - one "refuse.depot" record per depots entry (SORTED by depot ID,
//     GR#21): a registered depot (the map value is always true);
//   - one "refuse.site" record per sites entry (SORTED by site ID, GR#21):
//     the disposal site's full mutable state — kind, capacity, permanent
//     `used` fill (the refuse-owned durable fill record that re-seeds the
//     logistics shelf after a Wire re-provision), the per-stream backlog,
//     the reclaimed flag, the surrounding cells, and the accumulated
//     incinerator energy / airshed and compost output; and
//   - one "refuse.strike" record per strike entry (SORTED by depot ID,
//     GR#21): a depot's strike-active flag (the map value is a real bool —
//     SetStrike may store false when a strike is cleared).
//
// Every wire projection carries explicit json tags: the domain structs
// (cellState / roundState / disposalSite) are never marshalled directly (the
// field-parity drift tests guard this).
//
// SaveParticipant does NOT import internal/engine/save: it satisfies
// save.Participant STRUCTURALLY (Kind/Source/Handler), consuming only
// internal/foundation/serialize's Record/RecordSource/RecordHandler
// vocabulary — keeping this package on its single registered
// engine.refuse→int.serializer edge.

const (
	// KindRefuse is this participant's stable shard label. Must be unique
	// across a participant list; save.Load matches it against the shard
	// header's Kind to route the shard back here.
	KindRefuse = "refuse"

	recRefuseMeta   = "refuse.meta"
	recRefuseCell   = "refuse.cell"
	recRefuseRound  = "refuse.round"
	recRefuseDepot  = "refuse.depot"
	recRefuseSite   = "refuse.site"
	recRefuseStrike = "refuse.strike"
)

// refuseMetaWire carries the RefuseAPI's scalar/array runtime state — every
// mutable field that is not one of the map-backed collections. Explicit json
// tags: the domain is never marshalled directly (the field-parity drift test
// guards this).
type refuseMetaWire struct {
	Generated       [3]int64 `json:"generated"`
	Collected       [3]int64 `json:"collected"`
	Contamination   float64  `json:"contamination"`
	GeneralSiteID   string   `json:"generalSiteID"`
	CompostSiteID   string   `json:"compostSiteID"`
	TrucksAvailable int64    `json:"trucksAvailable"`
}

// refuseCellWire is one cells entry on the wire: the cell ID (the flattened
// map key) plus the cell's full mutable bin state. missCause is carried by
// value (a defensive copy taken under the lock), so a load never aliases a
// live cellState field.
type refuseCellWire struct {
	CellID    string     `json:"cellID"`
	LandUse   LandUse    `json:"landUse"`
	Street    string     `json:"street"`
	Capacity  int64      `json:"capacity"`
	Levels    [3]int64   `json:"levels"`
	Overflow  [3]int64   `json:"overflow"`
	Vermin    float64    `json:"vermin"`
	MissCause *MissCause `json:"missCause"`
}

// refuseRoundWire is one rounds entry on the wire: the round ID (the
// flattened map key) plus the round's mutable schedule/route/override/
// in-transit state. The transient `active` flag is intentionally absent —
// see the field-parity drift test's roundState allowlist for why persisting
// a mid-call claim would leave the round permanently un-runnable after load.
type refuseRoundWire struct {
	ID            string   `json:"id"`
	DepotID       string   `json:"depotID"`
	Cells         []string `json:"cells"`
	Route         []string `json:"route"`
	Overridden    bool     `json:"overridden"`
	OverrideRoute []string `json:"overrideRoute"`
	Completed     bool     `json:"completed"`
	InTransit     [3]int64 `json:"inTransit"`
}

// refuseDepotWire is one depots entry on the wire: a registered depot ID.
// The map value is always true (RegisterDepot only ever sets true), so no
// bool is carried — the presence of the record IS the registration.
type refuseDepotWire struct {
	DepotID string `json:"depotID"`
}

// refuseSiteWire is one sites entry on the wire: the site ID (the flattened
// map key) plus the disposal site's full mutable state. `used` is the
// refuse-owned durable fill record that re-seeds the logistics shelf after a
// Wire re-provision, so it MUST round-trip (a lost fill would let a full
// landfill accept over capacity, breaking AC-8's monotone RemainingCapacity).
type refuseSiteWire struct {
	ID          string       `json:"id"`
	Kind        DisposalKind `json:"kind"`
	Capacity    int64        `json:"capacity"`
	Used        int64        `json:"used"`
	Backlog     [3]int64     `json:"backlog"`
	Reclaimed   bool         `json:"reclaimed"`
	Surrounding []string     `json:"surrounding"`
	Energy      int64        `json:"energy"`
	Airshed     float64      `json:"airshed"`
	Compost     int64        `json:"compost"`
}

// refuseStrikeWire is one strike entry on the wire: a depot ID plus its
// strike-active flag. Unlike depots, the value is a real bool — SetStrike
// stores false when a strike is cleared — so the flag is carried explicitly.
type refuseStrikeWire struct {
	DepotID string `json:"depotID"`
	Active  bool   `json:"active"`
}

// refuseSnapshot is a point-in-time, deterministically-ordered copy of the
// mutable state, taken under the lock in one shot. Every map-backed
// collection is flattened to a slice SORTED by key (GR#21). The emitted
// record order — and therefore the saved bytes — is deterministic.
type refuseSnapshot struct {
	meta    refuseMetaWire
	cells   []refuseCellWire   // sorted by cell ID
	rounds  []refuseRoundWire  // sorted by round ID
	depots  []refuseDepotWire  // sorted by depot ID
	sites   []refuseSiteWire   // sorted by site ID
	strikes []refuseStrikeWire // sorted by depot ID
}

// total is the number of records the snapshot emits: one meta record plus
// one per cell, round, depot, site, and strike entry.
func (s *refuseSnapshot) total() int {
	return 1 + len(s.cells) + len(s.rounds) + len(s.depots) + len(s.sites) + len(s.strikes)
}

// recordAt marshals exactly the i-th record of the deterministic emission
// sequence (meta, cells, rounds, depots, sites, strikes) — one record's
// bytes, on demand, so Source never materialises the whole encoded shard
// before its first yield.
func (s *refuseSnapshot) recordAt(i int) (serialize.Record, error) {
	kind, value := s.locate(i)
	data, err := json.Marshal(value)
	if err != nil {
		return serialize.Record{}, fmt.Errorf("refuse: marshalling save record %d (kind %q): %w", i, kind, err)
	}
	return serialize.Record{Kind: kind, Data: data}, nil
}

// locate maps a global record index to its (Kind, wire value) without
// encoding anything — the pure index arithmetic behind recordAt.
func (s *refuseSnapshot) locate(i int) (string, any) {
	if i == 0 {
		return recRefuseMeta, s.meta
	}
	i--
	if i < len(s.cells) {
		return recRefuseCell, s.cells[i]
	}
	i -= len(s.cells)
	if i < len(s.rounds) {
		return recRefuseRound, s.rounds[i]
	}
	i -= len(s.rounds)
	if i < len(s.depots) {
		return recRefuseDepot, s.depots[i]
	}
	i -= len(s.depots)
	if i < len(s.sites) {
		return recRefuseSite, s.sites[i]
	}
	i -= len(s.sites)
	return recRefuseStrike, s.strikes[i]
}

// snapshotForSave copies the full mutable state into a
// deterministically-ordered refuseSnapshot under the read lock. It reads
// everything in one locked pass so the snapshot is internally consistent,
// then releases the lock — Source encodes from the snapshot, not the live
// state.
func (r *RefuseAPI) snapshotForSave() (refuseSnapshot, error) {
	if err := r.checkNotCopied("snapshotForSave"); err != nil {
		return refuseSnapshot{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	snap := refuseSnapshot{
		meta: refuseMetaWire{
			Generated:       r.generated,
			Collected:       r.collected,
			Contamination:   r.contamination,
			GeneralSiteID:   r.generalSiteID,
			CompostSiteID:   r.compostSiteID,
			TrucksAvailable: r.trucksAvailable,
		},
	}

	// Cells — sorted by cell ID (GR#21). missCause copied defensively so the
	// wire value never aliases the live cellState pointer (SEC-138).
	cellIDs := make([]string, 0, len(r.cells))
	for id := range r.cells {
		cellIDs = append(cellIDs, id)
	}
	sort.Strings(cellIDs)
	snap.cells = make([]refuseCellWire, 0, len(cellIDs))
	for _, id := range cellIDs {
		c := r.cells[id]
		snap.cells = append(snap.cells, refuseCellWire{
			CellID:    id,
			LandUse:   c.landUse,
			Street:    c.street,
			Capacity:  c.capacity,
			Levels:    c.levels,
			Overflow:  c.overflow,
			Vermin:    c.vermin,
			MissCause: copyMissCause(c.missCause),
		})
	}

	// Rounds — sorted by round ID (GR#21). Slices copied so the wire never
	// aliases the live roundState slices.
	roundIDs := make([]string, 0, len(r.rounds))
	for id := range r.rounds {
		roundIDs = append(roundIDs, id)
	}
	sort.Strings(roundIDs)
	snap.rounds = make([]refuseRoundWire, 0, len(roundIDs))
	for _, id := range roundIDs {
		rd := r.rounds[id]
		snap.rounds = append(snap.rounds, refuseRoundWire{
			ID:            rd.id,
			DepotID:       rd.depotID,
			Cells:         append([]string(nil), rd.cells...),
			Route:         append([]string(nil), rd.route...),
			Overridden:    rd.overridden,
			OverrideRoute: append([]string(nil), rd.overrideRoute...),
			Completed:     rd.completed,
			InTransit:     rd.inTransit,
		})
	}

	// Depots — sorted by depot ID (GR#21).
	depotIDs := make([]string, 0, len(r.depots))
	for id := range r.depots {
		depotIDs = append(depotIDs, id)
	}
	sort.Strings(depotIDs)
	snap.depots = make([]refuseDepotWire, 0, len(depotIDs))
	for _, id := range depotIDs {
		snap.depots = append(snap.depots, refuseDepotWire{DepotID: id})
	}

	// Sites — sorted by site ID (GR#21). surrounding copied so the wire never
	// aliases the live disposalSite slice.
	siteIDs := make([]string, 0, len(r.sites))
	for id := range r.sites {
		siteIDs = append(siteIDs, id)
	}
	sort.Strings(siteIDs)
	snap.sites = make([]refuseSiteWire, 0, len(siteIDs))
	for _, id := range siteIDs {
		st := r.sites[id]
		snap.sites = append(snap.sites, refuseSiteWire{
			ID:          st.id,
			Kind:        st.kind,
			Capacity:    st.capacity,
			Used:        st.used,
			Backlog:     st.backlog,
			Reclaimed:   st.reclaimed,
			Surrounding: append([]string(nil), st.surrounding...),
			Energy:      st.energy,
			Airshed:     st.airshed,
			Compost:     st.compost,
		})
	}

	// Strikes — sorted by depot ID (GR#21).
	strikeDepots := make([]string, 0, len(r.strike))
	for id := range r.strike {
		strikeDepots = append(strikeDepots, id)
	}
	sort.Strings(strikeDepots)
	snap.strikes = make([]refuseStrikeWire, 0, len(strikeDepots))
	for _, id := range strikeDepots {
		snap.strikes = append(snap.strikes, refuseStrikeWire{DepotID: id, Active: r.strike[id]})
	}

	return snap, nil
}

// resetForLoad clears the mutable state to empty under the write lock, before
// a Load streams records in. A load must REPLACE the state with the saved
// one, so every serialized runtime scalar/array/map is reset here — Handler
// then rebuilds them one record at a time. The immutable config (cfg) and the
// injected dependencies (logistics/services/wellbeing) are left untouched:
// they are the same for a given data/refuse.json and are re-wired by the
// composition root, not part of a save.
//
// `provisioned` is reset here even though it is NOT serialized: a stale
// "provisioned against the previous logistics instance" flag must never
// survive a load, or ensureSiteShelf would skip re-provisioning the new
// instance's shelf and the restored `disposalSite.used` fill would be lost on
// the logistics side. Resetting it to empty forces a lazy re-provision that
// re-seeds the shelf from the durable `used` record (AC-8).
func (r *RefuseAPI) resetForLoad() error {
	if err := r.checkNotCopied("resetForLoad"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cells = make(map[string]*cellState)
	r.rounds = make(map[string]*roundState)
	r.depots = make(map[string]bool)
	r.sites = make(map[string]*disposalSite)
	r.generated = [3]int64{}
	r.collected = [3]int64{}
	r.contamination = 0
	r.generalSiteID = ""
	r.compostSiteID = ""
	r.trucksAvailable = 0
	r.strike = make(map[string]bool)
	r.provisioned = make(map[string]bool)
	return nil
}

// applyLoadRecord decodes one streamed record and installs its effect
// directly into the state under the write lock. Installing per record — rather
// than buffering the whole decoded shard and then assigning — keeps the load
// side O(1) per record and streaming, the mirror of Source's
// one-record-at-a-time emission. Returns a decode/kind error verbatim so
// ReadShard fails loud and closed rather than loading a partial state
// silently.
func (r *RefuseAPI) applyLoadRecord(rec serialize.Record) error {
	if err := r.checkNotCopied("applyLoadRecord"); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	switch rec.Kind {
	case recRefuseMeta:
		var m refuseMetaWire
		if err := json.Unmarshal(rec.Data, &m); err != nil {
			return fmt.Errorf("refuse: decoding %s record: %w", rec.Kind, err)
		}
		r.generated = m.Generated
		r.collected = m.Collected
		r.contamination = m.Contamination
		r.generalSiteID = m.GeneralSiteID
		r.compostSiteID = m.CompostSiteID
		r.trucksAvailable = m.TrucksAvailable

	case recRefuseCell:
		var w refuseCellWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("refuse: decoding %s record: %w", rec.Kind, err)
		}
		r.cells[w.CellID] = &cellState{
			landUse:   w.LandUse,
			street:    w.Street,
			capacity:  w.Capacity,
			levels:    w.Levels,
			overflow:  w.Overflow,
			vermin:    w.Vermin,
			missCause: copyMissCause(w.MissCause),
		}

	case recRefuseRound:
		var w refuseRoundWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("refuse: decoding %s record: %w", rec.Kind, err)
		}
		r.rounds[w.ID] = &roundState{
			id:            w.ID,
			depotID:       w.DepotID,
			cells:         append([]string(nil), w.Cells...),
			route:         append([]string(nil), w.Route...),
			overridden:    w.Overridden,
			overrideRoute: append([]string(nil), w.OverrideRoute...),
			completed:     w.Completed,
			inTransit:     w.InTransit,
			// active is intentionally left false: a save never persists a
			// mid-call re-entrancy claim.
		}

	case recRefuseDepot:
		var w refuseDepotWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("refuse: decoding %s record: %w", rec.Kind, err)
		}
		r.depots[w.DepotID] = true

	case recRefuseSite:
		var w refuseSiteWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("refuse: decoding %s record: %w", rec.Kind, err)
		}
		r.sites[w.ID] = &disposalSite{
			id:          w.ID,
			kind:        w.Kind,
			capacity:    w.Capacity,
			used:        w.Used,
			backlog:     w.Backlog,
			reclaimed:   w.Reclaimed,
			surrounding: append([]string(nil), w.Surrounding...),
			energy:      w.Energy,
			airshed:     w.Airshed,
			compost:     w.Compost,
		}

	case recRefuseStrike:
		var w refuseStrikeWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("refuse: decoding %s record: %w", rec.Kind, err)
		}
		r.strike[w.DepotID] = w.Active

	default:
		return fmt.Errorf("refuse: unknown refuse save record kind %q", rec.Kind)
	}
	return nil
}

// SaveParticipant adapts a *RefuseAPI to the save.Participant contract
// (Kind/Source/Handler) without this package importing engine/save — the
// interface is satisfied structurally. Construct via NewSaveParticipant; the
// wrapped RefuseAPI is the live state Source snapshots on save and the target
// Handler rebuilds on load.
type SaveParticipant struct {
	r *RefuseAPI
}

// NewSaveParticipant returns a SaveParticipant streaming/reconstructing r's
// state. On save it snapshots r; on load it resets r's runtime state and
// rebuilds it from the streamed records — so a load target is typically a
// FRESH Load of the same data/refuse.json whose runtime state is replaced by
// the saved one (then re-wired by the composition root via Wire).
func NewSaveParticipant(r *RefuseAPI) *SaveParticipant {
	// SEC-020 pre-lock guard (astgate live-tree): a copied RefuseAPI is still
	// wrapped so the caller gets a non-nil participant, but every method below
	// re-checks checkNotCopied and fails closed, so a copy can never actually
	// read or mutate the state through this participant.
	_ = r.checkNotCopied("NewSaveParticipant")
	return &SaveParticipant{r: r}
}

// Kind returns the refuse shard label. The SEC-020 guard mirrors every other
// method that reaches the wrapped candidate type (astgate live-tree): a copied
// RefuseAPI yields the empty kind, which save.Load and registry validation
// reject rather than routing a shard to a copy.
func (p *SaveParticipant) Kind() string {
	if err := p.r.checkNotCopied("Kind"); err != nil {
		return ""
	}
	return KindRefuse
}

// Source returns a fresh pull-iterator over the refuse state. It snapshots the
// full mutable state under the lock once, up front, then yields one record at
// a time, marshalling each on demand — never buffering the whole encoded shard
// before the first yield. A copied-value guard failure (SEC-020) surfaces on
// the first pull.
func (p *SaveParticipant) Source() serialize.RecordSource {
	if err := p.r.checkNotCopied("Source"); err != nil {
		return func() (serialize.Record, bool, error) { return serialize.Record{}, false, err }
	}
	snap, snapErr := p.r.snapshotForSave()
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

// Handler returns a fresh sink that rebuilds the refuse state from the
// streamed records. It clears the target's runtime state on the first record,
// then installs each record's effect directly under the lock — one record at a
// time, never buffering the whole shard.
func (p *SaveParticipant) Handler() serialize.RecordHandler {
	if err := p.r.checkNotCopied("Handler"); err != nil {
		return func(serialize.Record) error { return err }
	}
	reset := false
	return func(rec serialize.Record) error {
		if !reset {
			if err := p.r.resetForLoad(); err != nil {
				return err
			}
			reset = true
		}
		return p.r.applyLoadRecord(rec)
	}
}
