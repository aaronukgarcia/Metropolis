package attract

import (
	"encoding/json"
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-1972079947 — engine.attract implements the save.Participant
// contract (edge engine.attract→int.serializer), following the pattern
// FEAT-1972079941's engine.finance pilot established and every later
// module participant (build/unlocks/refuse/traffic/world/citizens/crime/
// market/consumption) has mirrored since.
//
// This closes a specific, previously-documented gap:
// TestLoadAt_KnownLimitation_AttractStateNotRestoredAcrossMonthBoundary
// (internal/engine/compose/save_loadat_test.go) named and PROVED that
// engine.attract's own internal momentum state (reputation,
// lastAdvancedMonth, nextMigrantID — api.go) had no save.Participant at
// all and was therefore silently NOT restored by Load/LoadAt: continuing
// to tick a LoadAt'd composition across a NEW calendar month boundary
// diverged from a never-stopped reference engine the instant
// ApplyMigration's monthly reputation-momentum/migrant-id path ran
// again. That test is flipped to a positive tick-continuity assertion
// (compose/save_loadat_test.go) as part of this same increment.
//
// Serialization here is DATA-ONLY, exactly like every other participant
// in this epic: engine.attract has NO foundation/det import at all
// (verified by grep — the package's only RNG-adjacent surface is the
// counter-based hash draws AC-12 describes, and those are STATELESS,
// deterministic functions of (seed, month, id, ...) inputs, recomputed
// fresh on every call — never a persisted cursor). AttractAPI's own
// `seed` field is fixed construction-time config (the world seed [New]
// was called with, itself reproduced from save.Context.WorldSeed by the
// composition root on every Load), not draw state, and is excluded below
// alongside the rest of AttractAPI's construction-time config.
//
// SaveParticipant does NOT import internal/engine/save: it satisfies
// save.Participant STRUCTURALLY (Kind/Source/Handler), consuming only
// internal/foundation/serialize's Record/RecordSource/RecordHandler
// vocabulary — keeping this package on its single registered
// engine.attract→int.serializer edge.

const (
	// KindAttract is this participant's stable shard label. save.Load
	// matches it against the shard header's Kind to route a loaded shard's
	// records back here.
	KindAttract = "attract"

	recAttractMeta = "attract.meta"
)

// reputationStateWire is reputationState's wire projection (AC-2). The
// domain struct is never marshalled directly — a field added to
// reputationState without a matching wire field is caught by the
// field-parity drift test (participant_test.go), not silently dropped.
type reputationStateWire struct {
	HasBaseline bool    `json:"hasBaseline"`
	Baseline    float64 `json:"baseline"`
	Value       float64 `json:"value"`
}

// attractMetaWire carries every mutable field of AttractAPI this
// participant persists: the reputation-momentum state (projected via
// reputationStateWire), the monthly-advance idempotency tracker
// (lastAdvancedMonth/hasAdvanced — migration.go's ApplyMigration reads
// these to decide whether this month's fundamentals have already been
// folded into reputation), and the deterministic migrant-id counter
// (nextMigrantID — migration.go's mintMigrantID). A citizen-ID collision
// after restore is the FEAT-169 class of bug: nextMigrantID MUST round-
// trip exactly so post-restore migrants never re-mint an id a pre-save
// migrant already holds (see participant_test.go's explicit collision
// test).
type attractMetaWire struct {
	Reputation        reputationStateWire `json:"reputation"`
	LastAdvancedMonth int64               `json:"lastAdvancedMonth"`
	HasAdvanced       bool                `json:"hasAdvanced"`
	NextMigrantID     uint64              `json:"nextMigrantID"`
}

// attractSnapshot is a point-in-time copy of AttractAPI's mutable runtime
// state, taken under the read lock in one shot. There is only ever a
// single meta record (no map- or slice-backed collection lives on
// AttractAPI today), so no GR#21 sort/flatten step is needed — but the
// shape mirrors every other participant's snapshot struct so a future
// collection (e.g. a per-migrant ledger) slots in beside meta without
// reworking the Source/Handler streaming machinery.
type attractSnapshot struct {
	meta attractMetaWire
}

// total is the number of records the snapshot emits: exactly one meta
// record, always emitted (even when nothing has advanced yet), so a load
// has a deterministic reset trigger and the record stream is never
// zero-length.
func (s *attractSnapshot) total() int {
	return 1
}

// recordAt marshals exactly the i-th record of the deterministic
// emission sequence (meta only, at v1) — one record's bytes, on demand,
// so Source never materialises the whole encoded shard before its first
// yield (AC-4).
func (s *attractSnapshot) recordAt(i int) (serialize.Record, error) {
	kind, value := s.locate(i)
	data, err := json.Marshal(value)
	if err != nil {
		return serialize.Record{}, fmt.Errorf("attract: marshalling save record %d (kind %q): %w", i, kind, err)
	}
	return serialize.Record{Kind: kind, Data: data}, nil
}

// locate maps a global record index to its (Kind, wire value) without
// encoding anything — the pure index arithmetic behind recordAt. Kept in
// this shape (rather than inlined into recordAt) so a future collection
// extends it the same way finance/build/crime do.
func (s *attractSnapshot) locate(i int) (string, any) {
	return recAttractMeta, s.meta
}

// snapshotForSave copies AttractAPI's mutable runtime state into an
// attractSnapshot under the read lock (AC-1/AC-3). It reads everything in
// one locked pass so the snapshot is internally consistent, then releases
// the lock — Source encodes from the snapshot, not the live state. A
// copied-value guard failure (SEC-020) is returned rather than reading
// through a struct-copied receiver.
func (a *AttractAPI) snapshotForSave() (attractSnapshot, error) {
	if err := a.checkNotCopied("snapshotForSave"); err != nil {
		return attractSnapshot{}, err
	}
	a.mu.RLock()
	defer a.mu.RUnlock()

	return attractSnapshot{
		meta: attractMetaWire{
			Reputation: reputationStateWire{
				HasBaseline: a.reputation.hasBaseline,
				Baseline:    a.reputation.baseline,
				Value:       a.reputation.value,
			},
			LastAdvancedMonth: a.lastAdvancedMonth,
			HasAdvanced:       a.hasAdvanced,
			NextMigrantID:     a.nextMigrantID,
		},
	}, nil
}

// resetForLoad clears the persisted runtime state to its zero value under
// the write lock, before a Load streams the meta record in (AC-1). This
// mirrors the state a freshly-constructed AttractAPI holds BEFORE the
// meta record is applied — [New] separately sets nextMigrantID to 1,
// which is why applyLoadRecord always installs a value (a saved counter
// is never left at this zero, even for a save taken before any migrant
// was ever minted, because [New]'s pre-load value would otherwise leak
// through).
func (a *AttractAPI) resetForLoad() error {
	if err := a.checkNotCopied("resetForLoad"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reputation = reputationState{}
	a.lastAdvancedMonth = 0
	a.hasAdvanced = false
	a.nextMigrantID = 0
	return nil
}

// applyLoadRecord decodes one streamed record and installs its effect
// directly into AttractAPI under the write lock (AC-1/AC-4). Returns a
// decode/kind error verbatim so ReadShard fails loud and closed rather
// than loading a partial state silently.
func (a *AttractAPI) applyLoadRecord(rec serialize.Record) error {
	if err := a.checkNotCopied("applyLoadRecord"); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	switch rec.Kind {
	case recAttractMeta:
		var m attractMetaWire
		if err := json.Unmarshal(rec.Data, &m); err != nil {
			return fmt.Errorf("attract: decoding %s record: %w", rec.Kind, err)
		}
		a.reputation = reputationState{
			hasBaseline: m.Reputation.HasBaseline,
			baseline:    m.Reputation.Baseline,
			value:       m.Reputation.Value,
		}
		a.lastAdvancedMonth = m.LastAdvancedMonth
		a.hasAdvanced = m.HasAdvanced
		a.nextMigrantID = m.NextMigrantID

	default:
		return fmt.Errorf("attract: unknown attract save record kind %q", rec.Kind)
	}
	return nil
}

// SaveParticipant adapts a *AttractAPI to the save.Participant contract
// (Kind/Source/Handler) without this package importing engine/save — the
// interface is satisfied structurally. Construct via NewSaveParticipant;
// the wrapped AttractAPI is the live state Source snapshots on save and
// the target Handler rebuilds on load.
type SaveParticipant struct {
	a *AttractAPI
}

// NewSaveParticipant returns a SaveParticipant streaming/reconstructing
// a's state. On save it snapshots a; on load it resets a's persisted
// fields and rebuilds them from the streamed meta record — every other
// AttractAPI field (weights/world/migrationRate/repCfg/seed and the
// citizens/finance/households dependency pointers) is construction-time
// config the composition root re-supplies via New/SetCitizens/
// SetFinance/SetHouseholds before Load ever runs, so a load target is
// typically a freshly-[New]-constructed, freshly-wired AttractAPI.
func NewSaveParticipant(a *AttractAPI) *SaveParticipant {
	// SEC-020 pre-lock guard (astgate live-tree): a copied AttractAPI is
	// still wrapped so the caller gets a non-nil participant, but every
	// method below re-checks checkNotCopied and fails closed, so a copy can
	// never actually read or mutate the state through this participant.
	_ = a.checkNotCopied("NewSaveParticipant")
	return &SaveParticipant{a: a}
}

// Kind returns the attract shard label (AC-1). The SEC-020 guard mirrors
// every other method that reaches the wrapped candidate type: a copied
// AttractAPI yields the empty kind, which save.Load and registry
// validation reject rather than routing a shard to a copy.
func (p *SaveParticipant) Kind() string {
	if err := p.a.checkNotCopied("Kind"); err != nil {
		return ""
	}
	return KindAttract
}

// Source returns a fresh pull-iterator over the attract state (AC-1). It
// snapshots the mutable state under the lock once, up front, then yields
// the single meta record — never buffering more than that one record
// before the first yield (AC-4). A copied-value guard failure (SEC-020)
// surfaces on the first pull.
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

// Handler returns a fresh sink that rebuilds the attract state from the
// streamed records (AC-1). It clears the target's persisted fields on the
// first record, then installs the meta record's effect directly under the
// lock (AC-4).
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
