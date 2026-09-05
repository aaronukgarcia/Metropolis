package firms

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// BUG-752 — engine.firms was NOT a save.Participant: compose/save_wire.go's
// Participants() had no firms entry, so a Load silently dropped the whole
// firm registry. Measured effect: BUG-745's AggregateOutputScale reverted to
// the neutral 1000 (every firm's OutputScale/CreditOutstanding/cash gone),
// the builders'-merchant firm re-registered under a NEW id (its OLD id,
// held by compose's simState.buildersMerchantFirmID, pointed at nothing),
// and a saved-then-loaded city diverged from a never-saved control by tens
// of millions of money over a short run. This file closes that gap,
// mirroring engine.finance's pilot pattern (participant.go) exactly:
// SaveParticipant is satisfied STRUCTURALLY — this file imports only
// internal/foundation/serialize (never internal/engine/save), so the new
// engine.firms -> int.serializer edge is the ONLY new import edge this
// change introduces (registered in code.json by the Architect before this
// landed, GR#25).
//
// Serialization here is DATA-ONLY, mirroring finance's documented
// reasoning: the engine RNG (internal/foundation/det) is stateless (every
// draw builds a fresh det.Stream from already-covered inputs and discards
// it), and FirmID minting itself is a PURE function of (seed, founder,
// month, purpose) via firmIDForLocked — seed comes from the save bundle's
// own header (compose's WorldSeed), founder/month/purpose travel with each
// firm's own founding record. There is therefore no "next FirmID counter"
// to persist (unlike finance's NextFirmID) — FirmID re-derivation from the
// restored founding facts is exactly reproducible.

const (
	// KindFirms is this participant's stable shard label (mirrors
	// finance's KindFinance). Unique across the composition's participant
	// list; save.Load routes the matching shard's records back here by
	// this Kind.
	KindFirms = "firms"

	// recMeta carries FirmsAPI's scalar/counter state.
	recMeta = "firms.meta"
	// recFirm is one registered firm (the registry entry itself).
	recFirm = "firms.registry"
	// recFounder is one founder-history ledger entry (AC-12).
	recFounder = "firms.founder"
	// recFoundedEvent is one CultureIndex founding-window entry.
	recFoundedEvent = "firms.foundedevent"
	// recEvent is one lifecycle event (Founded/Grown/Failed/Acquired).
	recEvent = "firms.event"
)

// Wire projections (mirrors finance/participant.go's AC-2 discipline): the
// domain structs are NEVER marshalled directly, so a field added to a
// domain type without a matching wire field is caught by the reflective
// field-parity drift test (participant_test.go), not silently dropped.

// premisesWire is Premises' wire projection.
type premisesWire struct {
	Secured   bool   `json:"secured"`
	ZoneClass string `json:"zoneClass"`
}

// financialWire is Financial's wire projection.
type financialWire struct {
	CreditOutstanding int64 `json:"creditOutstanding"`
	MonthlyCashFlow   int64 `json:"monthlyCashFlow"`
	OutputScale       int64 `json:"outputScale"`
}

// firmWire is Firm's wire projection — the "firms.registry" record.
type firmWire struct {
	ID               FirmID               `json:"id"`
	Name             string               `json:"name"`
	FounderCitizenID uint64               `json:"founderCitizenID"`
	Stage            Stage                `json:"stage"`
	Staff            []uint64             `json:"staff"`
	Sector           citizens.Sector      `json:"sector"`
	InputCommodity   market.CommodityType `json:"inputCommodity"`
	InputRequired    int64                `json:"inputRequired"`
	Premises         premisesWire         `json:"premises"`
	Stalled          bool                 `json:"stalled"`
	Financial        financialWire        `json:"financial"`
}

// founderRecordWire is one founderHistory map entry on the wire (the map
// key travels alongside as CitizenID).
type founderRecordWire struct {
	CitizenID uint64 `json:"citizenID"`
	Exited    bool   `json:"exited"`
}

// foundedEventWire is one foundedEvents (CultureIndex window) entry.
type foundedEventWire struct {
	FirmID FirmID `json:"firmID"`
	Month  int64  `json:"month"`
}

// lifecycleEventWire is one Events() log entry.
type lifecycleEventWire struct {
	Kind   LifecycleKind `json:"kind"`
	FirmID FirmID        `json:"firmID"`
	Month  int64         `json:"month"`
}

// firmsMetaWire carries FirmsAPI's scalar/counter state: the current month
// and the two churn counters (AC-9). totalCreditOutstanding is serialized
// directly (mirrors finance's TotalCreditLine/TotalDebt precedent) rather
// than re-derived on load, keeping the round trip byte-for-byte exact; the
// parity test independently proves it still reconciles with the sum of
// every restored firm's CreditOutstanding.
type firmsMetaWire struct {
	Month                  int64 `json:"month"`
	FoundedCount           int64 `json:"foundedCount"`
	FailedCount            int64 `json:"failedCount"`
	TotalCreditOutstanding int64 `json:"totalCreditOutstanding"`
}

// firmsSnapshot is a point-in-time, deterministically-ordered copy of the
// full firm registry, taken under the read lock in one shot (GR#21: every
// map-backed collection flattened to a slice sorted by key).
type firmsSnapshot struct {
	meta          firmsMetaWire
	firms         []firmWire          // sorted by FirmID (ascending)
	founders      []founderRecordWire // sorted by CitizenID
	foundedEvents []foundedEventWire  // append order (already deterministic)
	events        []lifecycleEventWire
}

func (s *firmsSnapshot) total() int {
	return 1 + len(s.firms) + len(s.founders) + len(s.foundedEvents) + len(s.events)
}

// recordAt marshals exactly the i-th record of the deterministic emission
// sequence (meta, firms, founders, foundedEvents, events) on demand — never
// buffering the whole encoded shard before the first yield.
func (s *firmsSnapshot) recordAt(i int) (serialize.Record, error) {
	kind, value := s.locate(i)
	data, err := json.Marshal(value)
	if err != nil {
		return serialize.Record{}, fmt.Errorf("firms: marshalling save record %d (kind %q): %w", i, kind, err)
	}
	return serialize.Record{Kind: kind, Data: data}, nil
}

func (s *firmsSnapshot) locate(i int) (string, any) {
	if i == 0 {
		return recMeta, s.meta
	}
	i--
	if i < len(s.firms) {
		return recFirm, s.firms[i]
	}
	i -= len(s.firms)
	if i < len(s.founders) {
		return recFounder, s.founders[i]
	}
	i -= len(s.founders)
	if i < len(s.foundedEvents) {
		return recFoundedEvent, s.foundedEvents[i]
	}
	i -= len(s.foundedEvents)
	return recEvent, s.events[i]
}

// toFirmWire projects a Firm (never marshalled directly).
func toFirmWire(f Firm) firmWire {
	return firmWire{
		ID:               f.ID,
		Name:             f.Name,
		FounderCitizenID: f.FounderCitizenID,
		Stage:            f.Stage,
		Staff:            append([]uint64(nil), f.Staff...),
		Sector:           f.Sector,
		InputCommodity:   f.InputCommodity,
		InputRequired:    f.InputRequired,
		Premises:         premisesWire{Secured: f.Premises.Secured, ZoneClass: f.Premises.ZoneClass},
		Stalled:          f.Stalled,
		Financial: financialWire{
			CreditOutstanding: f.Financial.CreditOutstanding,
			MonthlyCashFlow:   f.Financial.MonthlyCashFlow,
			OutputScale:       f.Financial.OutputScale,
		},
	}
}

// fromFirmWire rebuilds a Firm from its wire projection.
func fromFirmWire(w firmWire) Firm {
	return Firm{
		ID:               w.ID,
		Name:             w.Name,
		FounderCitizenID: w.FounderCitizenID,
		Stage:            w.Stage,
		Staff:            append([]uint64(nil), w.Staff...),
		Sector:           w.Sector,
		InputCommodity:   w.InputCommodity,
		InputRequired:    w.InputRequired,
		Premises:         Premises{Secured: w.Premises.Secured, ZoneClass: w.Premises.ZoneClass},
		Stalled:          w.Stalled,
		Financial: Financial{
			CreditOutstanding: w.Financial.CreditOutstanding,
			MonthlyCashFlow:   w.Financial.MonthlyCashFlow,
			OutputScale:       w.Financial.OutputScale,
		},
	}
}

// snapshotForSave copies the full registry into a deterministically-ordered
// firmsSnapshot under the read lock (mirrors finance's snapshotForSave).
func (f *FirmsAPI) snapshotForSave() (firmsSnapshot, error) {
	if err := f.checkNotCopied("snapshotForSave"); err != nil {
		return firmsSnapshot{}, err
	}
	f.mu.RLock()
	defer f.mu.RUnlock()

	snap := firmsSnapshot{
		meta: firmsMetaWire{
			Month:                  f.month,
			FoundedCount:           f.foundedCount,
			FailedCount:            f.failedCount,
			TotalCreditOutstanding: f.totalCreditOutstanding,
		},
	}

	// Firms — sorted by FirmID ascending (GR#21: no map-range emission).
	ids := make([]FirmID, 0, len(f.firms))
	for id := range f.firms {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	snap.firms = make([]firmWire, 0, len(ids))
	for _, id := range ids {
		snap.firms = append(snap.firms, toFirmWire(f.firms[id].firm))
	}

	// Founder history — sorted by CitizenID (GR#21).
	founderIDs := make([]uint64, 0, len(f.founderHistory))
	for cid := range f.founderHistory {
		founderIDs = append(founderIDs, cid)
	}
	sort.Slice(founderIDs, func(i, j int) bool { return founderIDs[i] < founderIDs[j] })
	snap.founders = make([]founderRecordWire, 0, len(founderIDs))
	for _, cid := range founderIDs {
		snap.founders = append(snap.founders, founderRecordWire{
			CitizenID: cid,
			Exited:    f.founderHistory[cid].exited,
		})
	}

	// foundedEvents/events are append-only logs in emission order — already
	// deterministic, no sort needed (GR#21 is about map ranges, not slices).
	snap.foundedEvents = make([]foundedEventWire, len(f.foundedEvents))
	for i, e := range f.foundedEvents {
		snap.foundedEvents[i] = foundedEventWire(e)
	}
	snap.events = make([]lifecycleEventWire, len(f.events))
	for i, e := range f.events {
		snap.events[i] = lifecycleEventWire(e)
	}

	return snap, nil
}

// resetForLoad clears the registry to empty under the write lock, before a
// Load streams records in (mirrors finance's resetForLoad). Deliberately
// does NOT touch the live-wiring pointers (citizens/finance/market/build),
// cfg, seed, correlationID, subscribers/nextSubID, or productivityModifier
// — none of those are part of the save (see participant_test.go's
// TestFirmsAPIFieldsAllClassified for the full excluded-field rationale).
func (f *FirmsAPI) resetForLoad() error {
	if err := f.checkNotCopied("resetForLoad"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.firms = make(map[FirmID]*firmState)
	f.totalCreditOutstanding = 0
	f.founderHistory = make(map[uint64]*founderRecord)
	f.foundedEvents = nil
	f.foundedCount = 0
	f.failedCount = 0
	f.month = 0
	f.events = nil
	return nil
}

// applyLoadRecord decodes one streamed record and installs its effect
// directly into the registry under the write lock (mirrors finance's
// applyLoadRecord — one record at a time, streaming both directions).
func (f *FirmsAPI) applyLoadRecord(rec serialize.Record) error {
	if err := f.checkNotCopied("applyLoadRecord"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()

	switch rec.Kind {
	case recMeta:
		var m firmsMetaWire
		if err := json.Unmarshal(rec.Data, &m); err != nil {
			return fmt.Errorf("firms: decoding %s record: %w", rec.Kind, err)
		}
		f.month = m.Month
		f.foundedCount = m.FoundedCount
		f.failedCount = m.FailedCount
		f.totalCreditOutstanding = m.TotalCreditOutstanding

	case recFirm:
		var w firmWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("firms: decoding %s record: %w", rec.Kind, err)
		}
		firm := fromFirmWire(w)
		f.firms[firm.ID] = &firmState{firm: firm}

	case recFounder:
		var w founderRecordWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("firms: decoding %s record: %w", rec.Kind, err)
		}
		f.founderHistory[w.CitizenID] = &founderRecord{exited: w.Exited}

	case recFoundedEvent:
		var w foundedEventWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("firms: decoding %s record: %w", rec.Kind, err)
		}
		f.foundedEvents = append(f.foundedEvents, foundedEvent(w))

	case recEvent:
		var w lifecycleEventWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("firms: decoding %s record: %w", rec.Kind, err)
		}
		f.events = append(f.events, LifecycleEvent(w))

	default:
		return fmt.Errorf("firms: unknown firms save record kind %q", rec.Kind)
	}
	return nil
}

// SaveParticipant adapts a *FirmsAPI to the save.Participant contract
// (Kind/Source/Handler) without this package importing engine/save — the
// interface is satisfied structurally (mirrors finance.SaveParticipant).
type SaveParticipant struct {
	f *FirmsAPI
}

// NewSaveParticipant returns a SaveParticipant streaming/reconstructing f's
// state. On save it snapshots f; on load it resets f and rebuilds it from
// the streamed records.
func NewSaveParticipant(f *FirmsAPI) *SaveParticipant {
	// SEC-020 pre-lock guard (astgate live-tree): mirrors finance's
	// NewSaveParticipant — a copied FirmsAPI is still wrapped so the caller
	// gets a non-nil participant, but every method below re-checks
	// checkNotCopied and fails closed.
	_ = f.checkNotCopied("NewSaveParticipant")
	return &SaveParticipant{f: f}
}

// Kind returns the firms shard label.
func (p *SaveParticipant) Kind() string {
	if err := p.f.checkNotCopied("Kind"); err != nil {
		return ""
	}
	return KindFirms
}

// Source returns a fresh pull-iterator over the firm registry. It snapshots
// the full registry under the lock once, up front, then yields one record
// at a time, marshalling each on demand.
func (p *SaveParticipant) Source() serialize.RecordSource {
	if err := p.f.checkNotCopied("Source"); err != nil {
		return func() (serialize.Record, bool, error) { return serialize.Record{}, false, err }
	}
	snap, snapErr := p.f.snapshotForSave()
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

// Handler returns a fresh sink that rebuilds the firm registry from the
// streamed records. It clears the target registry on the first record — a
// bundle with NO "firms" shard (every save taken before this participant
// existed) therefore never calls Handler at all, leaving the live registry
// exactly as Wire constructed it (empty); this is the documented
// old-saves-decode-to-empty-registry behaviour.
func (p *SaveParticipant) Handler() serialize.RecordHandler {
	if err := p.f.checkNotCopied("Handler"); err != nil {
		return func(serialize.Record) error { return err }
	}
	reset := false
	return func(rec serialize.Record) error {
		if !reset {
			if err := p.f.resetForLoad(); err != nil {
				return err
			}
			reset = true
		}
		return p.f.applyLoadRecord(rec)
	}
}
