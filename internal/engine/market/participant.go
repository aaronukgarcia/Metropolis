package market

import (
	"encoding/json"
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-1972079941 inc4 — engine.market implements the save.Participant
// contract (edge engine.market→int.serializer), mirroring the inc1
// engine.finance pilot, the inc2 engine.unlocks example, and the inc3
// engine.build example. It is the FOURTH engine module to save its state
// through the per-module serialization pattern.
//
// Serialization here is DATA-ONLY, like finance/unlocks/build:
// engine.market has NO foundation/det RNG at all (no det import — verified
// by grep; the only sync.* reference in the package is a sync.WaitGroup in
// a concurrency TEST that proves MarketAPI is safe for concurrent reads
// precisely BECAUSE it is immutable), so there is no mutable RNG cursor to
// persist. The reproducible-future inputs are (worldSeed, month) [in the
// save-bundle header]; a lossless save is exactly the module's mutable
// runtime state.
//
// ── The pivotal finding for THIS module: engine.market is STATELESS ──
//
// Unlike finance/unlocks/build (each of which holds a real mutable ledger/
// map + a lock + a SEC-020 copy-guard), MarketAPI is a PURE, immutable
// query surface. All three of its fields are excluded from the save for a
// documented reason (enumerated in the field-parity drift test):
//
//   - commodities   — immutable config, loaded once from data/market.json
//                     and never mutated after construction (AC-14: "the
//                     commodity map is populated once at construction and
//                     never mutated afterward"). Config is reloaded from the
//                     current data file on load, never restored from a save
//                     (the hard-reset-replay premise: a save must not pin
//                     old rules — FEAT-1972079897).
//   - pricingMode   — immutable config ("static" v1, AC-4/AC-18/ASM-489: a
//                     dynamic world price is out of scope for this module and
//                     lives in the separate feat.commoditymarket surface).
//   - correlationID — per-instance error correlation, not simulation state
//                     (excluded by finance/unlocks/build too).
//
// There is therefore, at v1, NO mutable runtime state to persist. A
// clearing price, per-commodity stock/inventory levels, or an id counter —
// the fields a stateful market WOULD carry — do not exist on MarketAPI
// today (ASM-489: "single static clearing price v1" is the static config
// value, not an evolving field).
//
// Rather than skip registration, this participant is built as the honest,
// forward-compatible placeholder the epic's field-parity obligation
// (participant.go:50-53) exists for: it emits a single "market.meta" record
// carrying the module's mutable SCALAR state — which is empty today — and
// the field-parity drift test (TestMarketAPIFieldsAllClassified) reddens
// the BUILD the instant a mutable field (e.g. a dynamic clearing price or a
// stock level) is added to MarketAPI without being serialized. That guard —
// the "built but not serialized" trap this whole inc exists to prevent — is
// the real value delivered here.
//
// SaveParticipant does NOT import internal/engine/save: it satisfies
// save.Participant STRUCTURALLY (Kind/Source/Handler), consuming only
// internal/foundation/serialize's Record/RecordSource/RecordHandler
// vocabulary — keeping this package on its single registered
// engine.market→int.serializer edge.
//
// No copy-guard (checkNotCopied) is present or needed: MarketAPI holds no
// sync.Locker, so a value copy is not a copylocks hazard (go vet / astgate
// live-tree find nothing to guard) — the SEC-020 guard the other three
// participants carry protects a copied mutex, which this struct does not
// have.

const (
	// KindMarket is this participant's stable shard label. Must be unique
	// across a participant list; save.Load matches it against the shard
	// header's Kind to route the shard back here.
	KindMarket = "market"

	recMarketMeta = "market.meta"
)

// marketMetaWire carries MarketAPI's mutable SCALAR runtime state. It is
// intentionally EMPTY at v1: engine.market is a stateless query surface
// (see this file's package-level design note) with no clearing price, stock
// level, or id counter to persist. It exists as the single, stable home for
// the first mutable scalar this module ever grows — at which point the
// field-parity drift test forces that scalar to be added here, and this
// wire (with its explicit json tags) carries it. The domain is never
// marshalled directly (the field-parity drift test guards that too).
type marketMetaWire struct{}

// marketSnapshot is a point-in-time copy of the mutable state. For a
// stateless module this holds only the (empty) meta record, but the shape
// mirrors finance/unlocks/build so a future stateful market slots its
// sorted, map-flattened collections in beside meta without reworking the
// Source/Handler streaming machinery.
type marketSnapshot struct {
	meta marketMetaWire
}

// total is the number of records the snapshot emits: exactly one meta
// record (always emitted, even when empty, so a load has a deterministic
// reset trigger and the record stream is never zero-length).
func (s *marketSnapshot) total() int {
	return 1
}

// recordAt marshals exactly the i-th record of the deterministic emission
// sequence (currently: meta only) — one record's bytes, on demand, so
// Source never materialises the whole encoded shard before its first yield.
func (s *marketSnapshot) recordAt(i int) (serialize.Record, error) {
	kind, value := s.locate(i)
	data, err := json.Marshal(value)
	if err != nil {
		return serialize.Record{}, fmt.Errorf("market: marshalling save record %d (kind %q): %w", i, kind, err)
	}
	return serialize.Record{Kind: kind, Data: data}, nil
}

// locate maps a global record index to its (Kind, wire value) without
// encoding anything — the pure index arithmetic behind recordAt.
func (s *marketSnapshot) locate(i int) (string, any) {
	// Only the meta record exists today; index arithmetic is trivial but
	// kept in this shape so future sorted collections extend it the same
	// way finance/unlocks/build do.
	return recMarketMeta, s.meta
}

// snapshotForSave copies the mutable state into a marketSnapshot. There is
// no lock to take (MarketAPI holds none — it is immutable after
// construction) and no mutable field to read; the snapshot is the empty
// meta record. Returns an error only for signature parity with the stateful
// participants (a future stateful market would surface a copy-guard failure
// here).
func (m *MarketAPI) snapshotForSave() (marketSnapshot, error) {
	return marketSnapshot{meta: marketMetaWire{}}, nil
}

// resetForLoad clears the mutable runtime state before a Load streams
// records in. engine.market has no mutable runtime state (only immutable
// config, which a load must NOT overwrite — it is reloaded from the current
// data file, never restored from a save), so this is a documented no-op.
// It stays in the shape the stateful participants use so a future mutable
// field is reset here rather than silently merged on load.
func (m *MarketAPI) resetForLoad() error {
	// Intentionally empty: no mutable state to reset. The immutable config
	// (commodities/pricingMode) and per-instance correlationID are left
	// untouched by design.
	return nil
}

// applyLoadRecord decodes one streamed record and installs its effect into
// the state. The meta record is decoded (validating the wire shape and
// rejecting a corrupt payload) but installs nothing today — there is no
// mutable scalar to assign. An unrecognised kind fails loud and closed so a
// forward-incompatible or corrupt shard is a hard error, never a silent
// partial load.
func (m *MarketAPI) applyLoadRecord(rec serialize.Record) error {
	switch rec.Kind {
	case recMarketMeta:
		var meta marketMetaWire
		if err := json.Unmarshal(rec.Data, &meta); err != nil {
			return fmt.Errorf("market: decoding %s record: %w", rec.Kind, err)
		}
		// No mutable scalar to install at v1 (stateless module). A future
		// mutable field is assigned from meta here.
		return nil

	default:
		return fmt.Errorf("market: unknown market save record kind %q", rec.Kind)
	}
}

// SaveParticipant adapts a *MarketAPI to the save.Participant contract
// (Kind/Source/Handler) without this package importing engine/save — the
// interface is satisfied structurally. Construct via NewSaveParticipant; the
// wrapped MarketAPI is the (config-only) state Source snapshots on save and
// the target Handler validates/rebuilds on load.
type SaveParticipant struct {
	m *MarketAPI
}

// NewSaveParticipant returns a SaveParticipant streaming/reconstructing m's
// state. engine.market carries no mutable runtime state at v1, so on save it
// emits an empty meta record and on load it validates the streamed records
// without mutating m's immutable config — a load target is a FRESH Load of
// the current data/market.json whose config is authoritative.
func NewSaveParticipant(m *MarketAPI) *SaveParticipant {
	return &SaveParticipant{m: m}
}

// Kind returns the market shard label. save.Load matches it against the
// shard header's Kind to route a loaded shard's records back here.
func (p *SaveParticipant) Kind() string {
	return KindMarket
}

// Source returns a fresh pull-iterator over the market state. It snapshots
// the (empty) mutable state up front, then yields one record at a time,
// marshalling each on demand — never buffering the whole encoded shard
// before the first yield.
func (p *SaveParticipant) Source() serialize.RecordSource {
	snap, snapErr := p.m.snapshotForSave()
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

// Handler returns a fresh sink that validates the streamed market records.
// It clears the target's (empty) runtime state on the first record, then
// applies each record's effect directly — one record at a time, never
// buffering the whole shard. An unrecognised record kind is rejected.
func (p *SaveParticipant) Handler() serialize.RecordHandler {
	reset := false
	return func(rec serialize.Record) error {
		if !reset {
			if err := p.m.resetForLoad(); err != nil {
				return err
			}
			reset = true
		}
		return p.m.applyLoadRecord(rec)
	}
}
