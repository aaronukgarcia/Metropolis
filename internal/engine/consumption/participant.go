package consumption

import (
	"encoding/json"
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-1972079941 inc7 — engine.consumption implements the save.Participant
// contract (edge engine.consumption→int.serializer), mirroring the inc1
// engine.finance pilot, the inc2 engine.unlocks example, the inc3
// engine.build example, the inc4 engine.market example, the inc5
// engine.refuse example, and the inc6 engine.traffic example. It is the
// SEVENTH engine module to save its state through the per-module
// serialization pattern.
//
// Serialization here is DATA-ONLY, like every prior inc: engine.consumption
// has NO foundation/det RNG at all (no det import — verified by grep) and no
// sync.* / atomic.* of any kind (verified by grep), so there is neither a
// mutable RNG cursor nor a lock to persist. The reproducible-future inputs
// are (worldSeed, month) [in the save-bundle header]; a lossless save is
// exactly the module's mutable runtime state.
//
// ── The pivotal finding for THIS module: engine.consumption is STATELESS ──
//
// Like engine.market (inc4), the [UtilityAPI] is a PURE, immutable query
// surface. All four of its fields are excluded from the save for a
// documented reason (enumerated in the field-parity drift test):
//
//   - consumption   — immutable config, the coefficient model loaded once
//                     from data/consumption.json (api.go's AC-17 note: "its
//                     loaded coefficient/season/market maps are populated once
//                     at Load and never mutated afterward"). Config is
//                     reloaded from the current data file on load, never
//                     restored from a save (the hard-reset-replay premise: a
//                     save must not pin old rules — FEAT-1972079897).
//   - season        — an INJECTED DEPENDENCY (*season.SeasonAPI), re-wired by
//                     the composition root on load, not simulation state this
//                     module owns.
//   - market        — an INJECTED DEPENDENCY (*market.MarketAPI), re-wired by
//                     the composition root on load, not simulation state this
//                     module owns.
//   - correlationID — per-instance error correlation, not simulation state
//                     (excluded by every prior participant too).
//
// There is therefore, at v1, NO mutable runtime state to persist on
// UtilityAPI. The demand-query methods ([ResidentialDemand]/[ClassDemand]/
// [BilledAmount] …) are pure after construction (api.go: "same inputs, same
// outputs, no side effects").
//
// Where the module's REAL durable state lives — and why it is NOT this
// participant's job: the mutable runtime state in this package
// ([Network.lastSolve], and an over-abstracted [AquiferYield.current]) lives
// on Network / AquiferYield VALUES that are constructed and OWNED by the
// caller — the composition root holds waterNet/powerNet/gasNet
// (compose.go), not the UtilityAPI. Network.lastSolve is furthermore
// DERIVED — it is recomputed every tick by [UtilityAPI.SolveDailyTick]. A
// per-module participant snapshots the module's own API state (the
// refuse/traffic pattern wraps the API that HOLDS the maps); consumption's
// API holds none, so persisting the compose root's Networks is the compose
// root's concern, not this edge's.
//
// Rather than skip registration, this participant is built as the honest,
// forward-compatible placeholder the epic's field-parity obligation exists
// for (exactly as engine.market chose): it emits a single "consumption.meta"
// record carrying the module's mutable SCALAR state — which is empty today —
// and the field-parity drift test (TestUtilityAPIFieldsAllClassified)
// reddens the BUILD the instant a mutable field (e.g. an accumulated
// per-city billing counter, or the Networks moved to live inside UtilityAPI)
// is added to UtilityAPI without being serialized. That guard — the "built
// but not serialized" trap this whole inc exists to prevent — is the real
// value delivered here.
//
// SaveParticipant does NOT import internal/engine/save: it satisfies
// save.Participant STRUCTURALLY (Kind/Source/Handler), consuming only
// internal/foundation/serialize's Record/RecordSource/RecordHandler
// vocabulary — keeping this package on its single registered
// engine.consumption→int.serializer edge.
//
// No copy-guard (checkNotCopied) is present or needed: UtilityAPI holds no
// sync.Locker, so a value copy is not a copylocks hazard (go vet / astgate
// live-tree find nothing to guard) — the SEC-020 guard the stateful
// participants carry protects a copied mutex, which this struct does not
// have.

const (
	// KindConsumption is this participant's stable shard label. Must be unique
	// across a participant list; save.Load matches it against the shard
	// header's Kind to route the shard back here.
	KindConsumption = "consumption"

	recConsumptionMeta = "consumption.meta"
)

// consumptionMetaWire carries UtilityAPI's mutable SCALAR runtime state. It
// is intentionally EMPTY at v1: engine.consumption is a stateless query
// surface (see this file's package-level design note) with no accumulated
// counter, cached solve, or id to persist. It exists as the single, stable
// home for the first mutable scalar this module ever grows — at which point
// the field-parity drift test forces that scalar to be added here, and this
// wire (with its explicit json tags) carries it. The domain is never
// marshalled directly (the field-parity drift test guards that too).
type consumptionMetaWire struct{}

// consumptionSnapshot is a point-in-time copy of the mutable state. For a
// stateless module this holds only the (empty) meta record, but the shape
// mirrors the stateful participants so a future stateful consumption slots
// its sorted, map-flattened collections in beside meta without reworking the
// Source/Handler streaming machinery.
type consumptionSnapshot struct {
	meta consumptionMetaWire
}

// total is the number of records the snapshot emits: exactly one meta record
// (always emitted, even when empty, so a load has a deterministic reset
// trigger and the record stream is never zero-length).
func (s *consumptionSnapshot) total() int {
	return 1
}

// recordAt marshals exactly the i-th record of the deterministic emission
// sequence (currently: meta only) — one record's bytes, on demand, so Source
// never materialises the whole encoded shard before its first yield.
func (s *consumptionSnapshot) recordAt(i int) (serialize.Record, error) {
	kind, value := s.locate(i)
	data, err := json.Marshal(value)
	if err != nil {
		return serialize.Record{}, fmt.Errorf("consumption: marshalling save record %d (kind %q): %w", i, kind, err)
	}
	return serialize.Record{Kind: kind, Data: data}, nil
}

// locate maps a global record index to its (Kind, wire value) without
// encoding anything — the pure index arithmetic behind recordAt.
func (s *consumptionSnapshot) locate(i int) (string, any) {
	// Only the meta record exists today; index arithmetic is trivial but kept
	// in this shape so future sorted collections extend it the same way the
	// stateful participants do.
	return recConsumptionMeta, s.meta
}

// snapshotForSave copies the mutable state into a consumptionSnapshot. There
// is no lock to take (UtilityAPI holds none — it is immutable after
// construction) and no mutable field to read; the snapshot is the empty meta
// record. Returns an error only for signature parity with the stateful
// participants (a future stateful consumption would surface a copy-guard
// failure here).
func (a *UtilityAPI) snapshotForSave() (consumptionSnapshot, error) {
	return consumptionSnapshot{meta: consumptionMetaWire{}}, nil
}

// resetForLoad clears the mutable runtime state before a Load streams records
// in. engine.consumption has no mutable runtime state (only immutable config,
// which a load must NOT overwrite — it is reloaded from the current data
// file, never restored from a save, and injected dependencies re-wired by the
// composition root), so this is a documented no-op. It stays in the shape the
// stateful participants use so a future mutable field is reset here rather
// than silently merged on load.
func (a *UtilityAPI) resetForLoad() error {
	// Intentionally empty: no mutable state to reset. The immutable config
	// (consumption), the injected dependencies (season/market), and the
	// per-instance correlationID are left untouched by design.
	return nil
}

// applyLoadRecord decodes one streamed record and installs its effect into
// the state. The meta record is decoded (validating the wire shape and
// rejecting a corrupt payload) but installs nothing today — there is no
// mutable scalar to assign. An unrecognised kind fails loud and closed so a
// forward-incompatible or corrupt shard is a hard error, never a silent
// partial load.
func (a *UtilityAPI) applyLoadRecord(rec serialize.Record) error {
	switch rec.Kind {
	case recConsumptionMeta:
		var meta consumptionMetaWire
		if err := json.Unmarshal(rec.Data, &meta); err != nil {
			return fmt.Errorf("consumption: decoding %s record: %w", rec.Kind, err)
		}
		// No mutable scalar to install at v1 (stateless module). A future
		// mutable field is assigned from meta here.
		return nil

	default:
		return fmt.Errorf("consumption: unknown consumption save record kind %q", rec.Kind)
	}
}

// SaveParticipant adapts a *UtilityAPI to the save.Participant contract
// (Kind/Source/Handler) without this package importing engine/save — the
// interface is satisfied structurally. Construct via NewSaveParticipant; the
// wrapped UtilityAPI is the (config-only) state Source snapshots on save and
// the target Handler validates/rebuilds on load.
type SaveParticipant struct {
	a *UtilityAPI
}

// NewSaveParticipant returns a SaveParticipant streaming/reconstructing a's
// state. engine.consumption carries no mutable runtime state at v1, so on save
// it emits an empty meta record and on load it validates the streamed records
// without mutating a's immutable config or injected dependencies — a load
// target is a FRESH Load of the current data/consumption.json (re-wired by the
// composition root) whose config is authoritative.
func NewSaveParticipant(a *UtilityAPI) *SaveParticipant {
	return &SaveParticipant{a: a}
}

// Kind returns the consumption shard label. save.Load matches it against the
// shard header's Kind to route a loaded shard's records back here.
func (p *SaveParticipant) Kind() string {
	return KindConsumption
}

// Source returns a fresh pull-iterator over the consumption state. It
// snapshots the (empty) mutable state up front, then yields one record at a
// time, marshalling each on demand — never buffering the whole encoded shard
// before the first yield.
func (p *SaveParticipant) Source() serialize.RecordSource {
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

// Handler returns a fresh sink that validates the streamed consumption
// records. It clears the target's (empty) runtime state on the first record,
// then applies each record's effect directly — one record at a time, never
// buffering the whole shard. An unrecognised record kind is rejected.
func (p *SaveParticipant) Handler() serialize.RecordHandler {
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
