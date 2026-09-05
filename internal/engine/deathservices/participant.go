package deathservices

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// BUG-689 — engine.deathservices implements the save.Participant contract
// (edge engine.deathservices -> int.serializer, registered e2927b5),
// mirroring the market/build/citizens precedent exactly: no import of
// internal/engine/save, the contract satisfied structurally via
// foundation/serialize's Record/RecordSource/RecordHandler vocabulary.
//
// This closes the AC-20 round's second built-but-not-wired half: MOD-083
// landed with real mutable state (bodies, cemetery plots, crematorium
// counters, hearse/dispensation month budgets) and NO save participant at
// all -- a save/restore boundary silently dropped every body record and
// the handoff cursor, which (per the round's own warning) would have
// double-delivered the entire handoff stream on the next restore once
// compose actually started calling Intake.
//
// Serialized state (every mutable field, verified against api.go/
// cemetery.go/crematory.go/hearse.go/dispensation.go):
//
//   - one "deathservices.meta" record (always emitted first): releasedTotal
//     (AC-14's independently-sourced BodiesReleased term -- MUST round-trip
//     or a restored module's conservation identity would desync from its
//     own body map), handoffCursor (BUG-689's exactly-once paging
//     watermark -- see api.go's IntakeFromHandoff doc for why dropping this
//     on restore would double-deliver the whole handoff stream),
//     negativeBudgetWarned (the once-only diagnostic latch), and the
//     hearse/dispensation month-scoped counters (lastMonth/usedThisMonth
//     each) so a restored module does not silently re-open this month's
//     budget mid-month.
//   - one "deathservices.body" record per body (SORTED by citizenID,
//     GR#21): every Body field, including the terminal disposal ids
//     (cemeteryID/crematoriumID) -- AC-15's terminal classification is
//     permanent and must survive a restore unchanged.
//   - one "deathservices.cemetery" record per registered cemetery (SORTED
//     by cemetery id, GR#21): capacity plus every plot slot in array
//     order (the array's own index IS a plot's identity -- never
//     resorted).
//   - one "deathservices.crematorium" record per registered crematorium
//     (SORTED by crematorium id, GR#21).
//
// NOT serialized: servicesAPI/logisticsAPI (injected dependencies,
// re-wired by the composition root on every Wire/Load, never a save
// concern -- mirrors build's world/season/logistics exclusion) and the
// correlationID/mu/self bookkeeping fields (per-instance, not simulation
// state).

const (
	// KindDeathServices is this participant's stable shard label. Must be
	// unique across a participant list; save.Load matches it against the
	// shard header's Kind to route a loaded shard's records back here.
	KindDeathServices = "deathservices"

	recDeathServicesMeta        = "deathservices.meta"
	recDeathServicesBody        = "deathservices.body"
	recDeathServicesCemetery    = "deathservices.cemetery"
	recDeathServicesCrematorium = "deathservices.crematorium"
)

// deathServicesMetaWire carries DeathServicesAPI's scalar runtime state --
// every mutable field that is not one of the three id-keyed maps.
type deathServicesMetaWire struct {
	ReleasedTotal        int64 `json:"releasedTotal"`
	HandoffCursor        int64 `json:"handoffCursor"`
	NegativeBudgetWarned bool  `json:"negativeBudgetWarned"`

	HearseLastMonth     int64 `json:"hearseLastMonth"`
	HearseUsedThisMonth int64 `json:"hearseUsedThisMonth"`

	DispensationActive        bool  `json:"dispensationActive"`
	DispensationLastMonth     int64 `json:"dispensationLastMonth"`
	DispensationUsedThisMonth int64 `json:"dispensationUsedThisMonth"`
}

// deathServicesBodyWire is one bodies entry on the wire (the map key,
// citizenID, is carried in the record itself so Handler can reinsert it
// without a separate key channel).
type deathServicesBodyWire struct {
	CitizenID     uint64    `json:"citizenId"`
	DeathMonth    int64     `json:"deathMonth"`
	EmergencyFlag bool      `json:"emergencyFlag"`
	State         BodyState `json:"state"`
	CemeteryID    string    `json:"cemeteryId,omitempty"`
	CrematoriumID string    `json:"crematoriumId,omitempty"`
}

// deathServicesPlotWire is one fixed plot slot on the wire. Array order IS
// the plot's identity within its cemetery -- never resorted independently
// of the parent cemetery record's Plots slice order.
type deathServicesPlotWire struct {
	Occupied    bool   `json:"occupied"`
	BuriedMonth int64  `json:"buriedMonth"`
	BodyID      uint64 `json:"bodyId"`
}

// deathServicesCemeteryWire is one cemeteries entry on the wire.
type deathServicesCemeteryWire struct {
	ID       string                  `json:"id"`
	Capacity int64                   `json:"capacity"`
	Plots    []deathServicesPlotWire `json:"plots"`
}

// deathServicesCrematoriumWire is one crematoria entry on the wire.
type deathServicesCrematoriumWire struct {
	ID                  string  `json:"id"`
	LastDay             int64   `json:"lastDay"`
	CremToday           int64   `json:"cremToday"`
	CumulativeStaffLoad float64 `json:"cumulativeStaffLoad"`
}

// deathServicesSnapshot is a point-in-time, deterministically-ordered copy
// of the mutable state, taken under the lock in one shot. Both id-keyed
// map collections are flattened to slices SORTED by key (GR#21).
type deathServicesSnapshot struct {
	meta       deathServicesMetaWire
	bodies     []deathServicesBodyWire        // sorted by citizenID
	cemeteries []deathServicesCemeteryWire    // sorted by cemetery id
	crematoria []deathServicesCrematoriumWire // sorted by crematorium id
}

// total is the number of records the snapshot emits: one meta record plus
// one per body/cemetery/crematorium entry.
func (s *deathServicesSnapshot) total() int {
	return 1 + len(s.bodies) + len(s.cemeteries) + len(s.crematoria)
}

// locate maps a global record index to its (Kind, wire value) without
// encoding anything -- the pure index arithmetic behind recordAt.
func (s *deathServicesSnapshot) locate(i int) (string, any) {
	if i == 0 {
		return recDeathServicesMeta, s.meta
	}
	i--
	if i < len(s.bodies) {
		return recDeathServicesBody, s.bodies[i]
	}
	i -= len(s.bodies)
	if i < len(s.cemeteries) {
		return recDeathServicesCemetery, s.cemeteries[i]
	}
	i -= len(s.cemeteries)
	return recDeathServicesCrematorium, s.crematoria[i]
}

// recordAt marshals exactly the i-th record of the deterministic emission
// sequence (meta, bodies, cemeteries, crematoria) -- one record's bytes,
// on demand, so Source never materialises the whole encoded shard before
// its first yield.
func (s *deathServicesSnapshot) recordAt(i int) (serialize.Record, error) {
	kind, value := s.locate(i)
	data, err := json.Marshal(value)
	if err != nil {
		return serialize.Record{}, fmt.Errorf("deathservices: marshalling save record %d (kind %q): %w", i, kind, err)
	}
	return serialize.Record{Kind: kind, Data: data}, nil
}

// snapshotForSave copies the full mutable state into a
// deathServicesSnapshot under the read lock, in one locked pass so the
// snapshot is internally consistent.
func (d *DeathServicesAPI) snapshotForSave(correlationID string) (deathServicesSnapshot, error) {
	if err := d.checkNotCopied(correlationID, "snapshotForSave"); err != nil {
		return deathServicesSnapshot{}, err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	snap := deathServicesSnapshot{
		meta: deathServicesMetaWire{
			ReleasedTotal:             d.releasedTotal,
			HandoffCursor:             d.handoffCursor,
			NegativeBudgetWarned:      d.negativeBudgetWarned,
			HearseLastMonth:           d.hearse.lastMonth,
			HearseUsedThisMonth:       d.hearse.usedThisMonth,
			DispensationActive:        d.dispensation.active,
			DispensationLastMonth:     d.dispensation.lastMonth,
			DispensationUsedThisMonth: d.dispensation.usedThisMonth,
		},
	}

	// Bodies -- sorted by citizenID (GR#21).
	bodyIDs := make([]uint64, 0, len(d.bodies))
	for id := range d.bodies {
		bodyIDs = append(bodyIDs, id)
	}
	sort.Slice(bodyIDs, func(i, j int) bool { return bodyIDs[i] < bodyIDs[j] })
	snap.bodies = make([]deathServicesBodyWire, 0, len(bodyIDs))
	for _, id := range bodyIDs {
		b := d.bodies[id]
		snap.bodies = append(snap.bodies, deathServicesBodyWire{
			CitizenID:     b.citizenID,
			DeathMonth:    b.deathMonth,
			EmergencyFlag: b.emergencyFlag,
			State:         b.state,
			CemeteryID:    b.cemeteryID,
			CrematoriumID: b.crematoriumID,
		})
	}

	// Cemeteries -- sorted by id (GR#21). Plot slice order is the plot's
	// own identity, never resorted.
	cemeteryIDs := make([]string, 0, len(d.cemeteries))
	for id := range d.cemeteries {
		cemeteryIDs = append(cemeteryIDs, id)
	}
	sort.Strings(cemeteryIDs)
	snap.cemeteries = make([]deathServicesCemeteryWire, 0, len(cemeteryIDs))
	for _, id := range cemeteryIDs {
		c := d.cemeteries[id]
		plots := make([]deathServicesPlotWire, len(c.plots))
		for i, p := range c.plots {
			plots[i] = deathServicesPlotWire{Occupied: p.occupied, BuriedMonth: p.buriedMonth, BodyID: p.bodyID}
		}
		snap.cemeteries = append(snap.cemeteries, deathServicesCemeteryWire{ID: c.id, Capacity: c.capacity, Plots: plots})
	}

	// Crematoria -- sorted by id (GR#21).
	crematoriumIDs := make([]string, 0, len(d.crematoria))
	for id := range d.crematoria {
		crematoriumIDs = append(crematoriumIDs, id)
	}
	sort.Strings(crematoriumIDs)
	snap.crematoria = make([]deathServicesCrematoriumWire, 0, len(crematoriumIDs))
	for _, id := range crematoriumIDs {
		cr := d.crematoria[id]
		snap.crematoria = append(snap.crematoria, deathServicesCrematoriumWire{
			ID: cr.id, LastDay: cr.lastDay, CremToday: cr.cremToday, CumulativeStaffLoad: cr.cumulativeStaffLoad,
		})
	}

	return snap, nil
}

// resetForLoad clears the mutable state to empty under the write lock,
// before a Load streams records in. The immutable config (cfg, loaded from
// data/deathservices.json) and the injected dependencies (servicesAPI/
// logisticsAPI, re-wired by the composition root) are left untouched --
// mirrors build's identical resetForLoad discipline.
// ResetForLoad is resetForLoad's exported form (BUG-689 round follow-up
// F1): save.Manager.Load only invokes a Participant's Handler for shards
// its bundle header actually LISTS (internal/engine/save/load.go's shard
// loop ranges over header.ShardIndex, never over the registered
// participant set) — so a bundle with NO deathservices shard (every
// pre-BUG-689 save) never calls Handler() at all, and Handler()'s own
// eager-on-construction reset (see Handler's doc) therefore never runs
// either. The composition root calls this directly, unconditionally,
// before invoking save.Manager.Load (compose's save_wire.go Load method),
// so "no deathservices shard present" reliably means "clean module state"
// regardless of what the bundle does or does not contain — the fix cannot
// live inside this package alone, since this package cannot see whether
// its own shard will be present in a bundle it has not read yet.
func (d *DeathServicesAPI) ResetForLoad(correlationID string) error {
	return d.resetForLoad(correlationID)
}

func (d *DeathServicesAPI) resetForLoad(correlationID string) error {
	if err := d.checkNotCopied(correlationID, "resetForLoad"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.bodies = make(map[uint64]*bodyRecord)
	d.cemeteries = make(map[string]*cemeteryState)
	d.crematoria = make(map[string]*crematoriumState)
	d.hearse = hearseState{}
	d.dispensation = dispensationState{}
	d.releasedTotal = 0
	d.handoffCursor = 0
	d.negativeBudgetWarned = false
	return nil
}

// applyLoadRecord decodes one streamed record and installs its effect
// directly into the state under the write lock. Returns a decode/kind
// error verbatim so a load fails loud and closed rather than silently
// installing a partial state.
func (d *DeathServicesAPI) applyLoadRecord(rec serialize.Record, correlationID string) error {
	if err := d.checkNotCopied(correlationID, "applyLoadRecord"); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	switch rec.Kind {
	case recDeathServicesMeta:
		var w deathServicesMetaWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("deathservices: decoding %s record: %w", rec.Kind, err)
		}
		d.releasedTotal = w.ReleasedTotal
		d.handoffCursor = w.HandoffCursor
		if d.handoffCursor < 0 {
			// BUG-689 round follow-up F6: a negative wire cursor is never
			// installed verbatim -- see ErrCorruptHandoffCursor's doc for
			// why an uncorrected negative base makes the module silently
			// re-read and re-discard the whole handoff stream every month
			// forever. Clamp to 0 (a safe, self-correcting re-delivery the
			// duplicate-death guard absorbs) and log once as a WARNING
			// (GR#17), never fatal -- a corrupt/hand-edited shard must
			// still decode.
			_ = errs.New(ErrCorruptHandoffCursor, correlationID, map[string]any{"handoffCursor": w.HandoffCursor})
			d.handoffCursor = 0
		}
		d.negativeBudgetWarned = w.NegativeBudgetWarned
		d.hearse.lastMonth = w.HearseLastMonth
		d.hearse.usedThisMonth = w.HearseUsedThisMonth
		d.dispensation.active = w.DispensationActive
		d.dispensation.lastMonth = w.DispensationLastMonth
		d.dispensation.usedThisMonth = w.DispensationUsedThisMonth

	case recDeathServicesBody:
		var w deathServicesBodyWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("deathservices: decoding %s record: %w", rec.Kind, err)
		}
		d.bodies[w.CitizenID] = &bodyRecord{
			citizenID:     w.CitizenID,
			deathMonth:    w.DeathMonth,
			emergencyFlag: w.EmergencyFlag,
			state:         w.State,
			cemeteryID:    w.CemeteryID,
			crematoriumID: w.CrematoriumID,
		}

	case recDeathServicesCemetery:
		var w deathServicesCemeteryWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("deathservices: decoding %s record: %w", rec.Kind, err)
		}
		plots := make([]plot, len(w.Plots))
		for i, p := range w.Plots {
			plots[i] = plot{occupied: p.Occupied, buriedMonth: p.BuriedMonth, bodyID: p.BodyID}
		}
		d.cemeteries[w.ID] = &cemeteryState{id: w.ID, capacity: w.Capacity, plots: plots}

	case recDeathServicesCrematorium:
		var w deathServicesCrematoriumWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("deathservices: decoding %s record: %w", rec.Kind, err)
		}
		d.crematoria[w.ID] = &crematoriumState{id: w.ID, lastDay: w.LastDay, cremToday: w.CremToday, cumulativeStaffLoad: w.CumulativeStaffLoad}

	default:
		return fmt.Errorf("deathservices: unknown deathservices save record kind %q", rec.Kind)
	}
	return nil
}

// SaveParticipant adapts a *DeathServicesAPI to the save.Participant
// contract (Kind/Source/Handler) without this package importing
// internal/engine/save -- the interface is satisfied structurally.
// Construct via NewSaveParticipant; the wrapped DeathServicesAPI is the
// live state Source snapshots on save and the target Handler rebuilds on
// load.
type SaveParticipant struct {
	d             *DeathServicesAPI
	correlationID string
}

// NewSaveParticipant returns a SaveParticipant streaming/reconstructing d's
// state. On save it snapshots d; on load it resets d's runtime state and
// rebuilds it from the streamed records -- a load target is typically a
// fresh Load of the current data/deathservices.json whose runtime state is
// replaced by the saved one.
func NewSaveParticipant(d *DeathServicesAPI) *SaveParticipant {
	cid := d.correlationID
	if cid == "" {
		cid = "deathservices-participant"
	}
	// SEC-020 pre-lock guard (astgate live-tree): a copied DeathServicesAPI
	// is still wrapped so the caller gets a non-nil participant, but every
	// method below re-checks checkNotCopied and fails closed, so a copy can
	// never actually read or mutate the state through this participant.
	_ = d.checkNotCopied(cid, "NewSaveParticipant")
	return &SaveParticipant{d: d, correlationID: cid}
}

// Kind returns the deathservices shard label. The SEC-020 guard mirrors
// every other method that reaches the wrapped candidate type: a copied
// DeathServicesAPI yields the empty kind, which save.Load and registry
// validation reject rather than routing a shard to a copy.
func (p *SaveParticipant) Kind() string {
	if err := p.d.checkNotCopied(p.correlationID, "Kind"); err != nil {
		return ""
	}
	return KindDeathServices
}

// Source returns a fresh pull-iterator over the deathservices state. It
// snapshots the full mutable state under the lock once, up front, then
// yields one record at a time, marshalling each on demand -- never
// buffering the whole encoded shard before the first yield.
func (p *SaveParticipant) Source() serialize.RecordSource {
	if err := p.d.checkNotCopied(p.correlationID, "Source"); err != nil {
		return func() (serialize.Record, bool, error) { return serialize.Record{}, false, err }
	}
	snap, snapErr := p.d.snapshotForSave(p.correlationID)
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

// Handler returns a fresh sink that rebuilds the deathservices state from
// the streamed records.
//
// BUG-689 round follow-up F1 (P2): resetForLoad runs EAGERLY here, at
// Handler construction, rather than lazily on the first record the way an
// earlier revision (and citizens' own Handler, participant.go) did it. A
// bundle written by a pre-BUG-689 binary carries NO deathservices shard at
// all, and save.Load only invokes a participant's Handler for shards its
// header actually lists -- a lazy reset therefore NEVER RUNS for a
// shard-less bundle, leaving this module's live state (cursor, body map)
// untouched while every OTHER module in the composition is reset and
// restored, desyncing deathservices from the very citizens handoff stream
// the restore just rewound. Resetting unconditionally, before any record
// (or the absence of one) is seen, makes "no shard present" mean "clean
// module state" regardless of what Load target is used -- mirroring the
// FEAT-087 empty-queue precedent this same round's shard-less-load test
// (TestAttackBUG689_OldSaveWithoutDeathServicesShardLoadsClean) already
// proves for the surrounding composition.
func (p *SaveParticipant) Handler() serialize.RecordHandler {
	if err := p.d.checkNotCopied(p.correlationID, "Handler"); err != nil {
		return func(serialize.Record) error { return err }
	}
	if err := p.d.resetForLoad(p.correlationID); err != nil {
		return func(serialize.Record) error { return err }
	}
	return func(rec serialize.Record) error {
		return p.d.applyLoadRecord(rec, p.correlationID)
	}
}
