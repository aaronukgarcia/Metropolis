package attract

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
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

	// migrantCounterCeiling is the plausibility ceiling applyLoadRecord
	// enforces on a decoded attract.meta record's NextMigrantID BEFORE
	// touching any state or running the tenure-map backfill loop (BUG-380
	// round finding P1, opus-round-bug380). Derived from the project's own
	// documented population ceiling (docs/METROPOLIS-MASTER-v2.1.md /
	// CLAUDE.md: "persistent individual citizens... up to 100M at adaptive
	// fidelity", Option B) — a migrant count can never exceed total
	// population, so any NextMigrantID beyond this is provably corrupt or
	// hostile input, never a legitimate large city. Without this check, a
	// hand-edited or corrupted save with NextMigrantID=1<<40 drove an
	// O(NextMigrantID) map-insert loop before any other validation ran —
	// attack_bug380_round_test.go's
	// TestAttack380_CorruptNextMigrantIDDrivesDecodeAllocation measured
	// ~45MB/160ms at a mere 1e6; 1<<40 would attempt roughly a
	// trillion-entry map and hang/OOM the process.
	migrantCounterCeiling = 100_000_000
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

// migrantTenureEntryWire is one migrantAdmittedMonth entry's wire
// projection (BUG-380 tenure grace).
type migrantTenureEntryWire struct {
	ID            uint64 `json:"id"`
	AdmittedMonth int64  `json:"admittedMonth"`
}

// attractMetaWire carries every mutable field of AttractAPI this
// participant persists: the reputation-momentum state (projected via
// reputationStateWire), the monthly-advance idempotency tracker
// (lastAdvancedMonth/hasAdvanced — migration.go's ApplyMigration reads
// these to decide whether this month's fundamentals have already been
// folded into reputation), the deterministic migrant-id counter
// (nextMigrantID — migration.go's mintMigrantID; a citizen-ID collision
// after restore is the FEAT-169 class of bug, so nextMigrantID MUST
// round-trip exactly, see participant_test.go's explicit collision test),
// and — BUG-380 (2026-09-05) — the migrant tenure-grace map
// (migrantAdmittedMonth), sorted by id (GR#21) so the JSON encoding is
// deterministic regardless of Go's map iteration order.
//
// MigrantAdmittedMonths is a POINTER (BUG-380 re-round finding P0,
// opus-reround-bug380), not a plain slice, so applyLoadRecord can tell
// "an OLD save that predates this field entirely" (decodes nil — the key
// is simply absent from the JSON) apart from "a MODERN save whose slice is
// authoritative, even when it legitimately holds fewer than
// NextMigrantID-1 entries because pruneMigrantTenure has already removed
// some" (decodes as a non-nil pointer to a slice — snapshotForSave always
// sets one, even to an empty slice, never leaves it nil). A plain
// (non-pointer) slice field cannot make this distinction: json.Unmarshal
// leaves BOTH "key absent" and "key present as []" as a nil Go slice,
// which is exactly the bug the re-round found — applyLoadRecord's old
// backfill loop treated a modern, ALREADY-PRUNED slice as if it were an
// old-shape record missing the field entirely, and re-inserted an entry
// for every migrant pruneMigrantTenure had legitimately removed, undoing
// the P2 prune on every save/load round trip (measured: 102 entries at a
// month-30 save became 208 after LoadAt).
//
// An OLD save (taken before BUG-380 landed at all) decodes this as nil —
// see applyLoadRecord's own comment for the backfill that ONLY runs in
// that case (a migrant's grace period re-starts from the load month, a
// conservative approximation, never a decode error).
type attractMetaWire struct {
	Reputation            reputationStateWire       `json:"reputation"`
	LastAdvancedMonth     int64                     `json:"lastAdvancedMonth"`
	HasAdvanced           bool                      `json:"hasAdvanced"`
	NextMigrantID         uint64                    `json:"nextMigrantID"`
	MigrantAdmittedMonths *[]migrantTenureEntryWire `json:"migrantAdmittedMonths,omitempty"`
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

	// Migrant tenure map -- sorted by id, numerically (GR#21), mirroring
	// engine.citizens' identical households/hot-fidelity sort pattern
	// (participant.go there).
	tenureIDs := make([]uint64, 0, len(a.migrantAdmittedMonth))
	for id := range a.migrantAdmittedMonth {
		tenureIDs = append(tenureIDs, id)
	}
	sort.Slice(tenureIDs, func(i, j int) bool { return tenureIDs[i] < tenureIDs[j] })
	tenureWire := make([]migrantTenureEntryWire, 0, len(tenureIDs))
	for _, id := range tenureIDs {
		tenureWire = append(tenureWire, migrantTenureEntryWire{ID: id, AdmittedMonth: a.migrantAdmittedMonth[id]})
	}

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
			// Always a non-nil pointer, even when tenureWire is empty
			// (zero migrants ever admitted) — see the field's own doc
			// comment for why this MUST never be left nil for a save this
			// code writes: a nil decode is what marks a record as
			// pre-BUG-380 legacy and triggers the backfill loop.
			MigrantAdmittedMonths: &tenureWire,
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
	// BUG-380: cleared to an EMPTY (never nil) map, not left at whatever a
	// pre-load AttractAPI happened to hold — applyLoadRecord always
	// installs the meta record's MigrantAdmittedMonths next (possibly
	// itself empty, for an old pre-BUG-380 save), mirroring nextMigrantID's
	// own "always installs a value" discipline above.
	a.migrantAdmittedMonth = make(map[uint64]int64)
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
		// BUG-380 round finding P1 (opus-round-bug380), re-round finding P2
		// (opus-reround-bug380): validate NextMigrantID against
		// migrantCounterCeiling BEFORE touching ANY state or running the
		// legacy tenure-map backfill loop below. Pre-fix, a corrupt/hostile
		// NextMigrantID (e.g. 1<<40 from a hand-edited or truncated save)
		// drove an O(NextMigrantID) map-insert loop with zero validation —
		// attack_bug380_round_test.go's
		// TestAttack380_CorruptNextMigrantIDDrivesDecodeAllocation measured
		// ~45MB/160ms at a mere 1e6 real entries. The check is `>=`, NOT
		// `>` (the re-round's own finding: a `>` check ACCEPTS the ceiling
		// value itself and still drives a ~1e8-iteration backfill —
		// TestReround380_CounterCeilingBoundary extrapolates that to
		// several GB of heap and tens of seconds, still a decode-time
		// hang/OOM at a merely higher trigger value). Refused HERE, before
		// the reputation/lastAdvancedMonth/hasAdvanced/nextMigrantID
		// assignments too, so a refused record leaves the target's state
		// exactly as it was after resetForLoad (zeroed) rather than a
		// partially-applied blend.
		if m.NextMigrantID >= migrantCounterCeiling {
			return errs.New(ErrMigrantCounterImplausible, a.correlationID, map[string]any{
				"nextMigrantID": m.NextMigrantID,
				"ceiling":       uint64(migrantCounterCeiling),
			})
		}
		a.reputation = reputationState{
			hasBaseline: m.Reputation.HasBaseline,
			baseline:    m.Reputation.Baseline,
			value:       m.Reputation.Value,
		}
		a.lastAdvancedMonth = m.LastAdvancedMonth
		a.hasAdvanced = m.HasAdvanced
		a.nextMigrantID = m.NextMigrantID

		// BUG-380 re-round finding P0 (opus-reround-bug380, BLOCKING): a
		// MODERN record's MigrantAdmittedMonths slice is AUTHORITATIVE —
		// pruneMigrantTenure (migration.go) may have already legitimately
		// removed entries for migrants this package itself emigrated, and
		// that pruned slice is exactly what got saved. The OLD code ran
		// the backfill loop unconditionally after installing the saved
		// entries, which re-inserted one at m.LastAdvancedMonth for EVERY
		// id the slice was missing — indistinguishable, to that loop, from
		// "a migrant pruneMigrantTenure legitimately removed" and "an id
		// an old pre-BUG-380 save never recorded at all". Every save/load
		// round trip therefore UNDID the P2 prune and put the map back on
		// its unbounded growth path (measured: 102 entries at a month-30
		// save became 208 after LoadAt — TestReround380_
		// PruningIsUndoneByTheOldSaveBackfill). Fixed by making
		// MigrantAdmittedMonths a POINTER (attractMetaWire's own doc
		// comment has the full nil-vs-non-nil rationale): a non-nil
		// pointer (every save this code writes, even with zero entries)
		// means "trust this slice completely, no backfill, ever" — the
		// backfill loop below runs ONLY when the pointer decodes nil, the
		// signature of a record from BEFORE this field existed at all.
		if m.MigrantAdmittedMonths != nil {
			for _, e := range *m.MigrantAdmittedMonths {
				a.migrantAdmittedMonth[e.ID] = e.AdmittedMonth
			}
		} else {
			// Legacy path: a pre-BUG-380 record never recorded ANY
			// migrant's admission month, so every real migrant id
			// (derivable purely from the just-restored nextMigrantID
			// counter, migrantIDHighBit's own three-package id map) is
			// backfilled at m.LastAdvancedMonth (the save's own
			// last-advanced month, the closest available proxy for "when
			// this state was captured") rather than 0 or the id's mint
			// context (unavailable at decode time): a migrant so
			// backfilled starts its tenure grace fresh from the save
			// point, which is the CONSERVATIVE direction (never wrongly
			// makes an already-eligible migrant instantly
			// emigration-eligible on load; at worst delays eligibility by
			// up to migrantTenureGraceMonths for a migrant that was
			// already past grace pre-save) — never a decode error or a
			// crash. a.migrantAdmittedMonth is guaranteed empty here
			// (resetForLoad, and this is a legacy record with nothing to
			// have installed above), so a plain assignment is correct —
			// no pruned-vs-legacy ambiguity can exist for a record that
			// predates pruning entirely.
			for i := uint64(2); i <= m.NextMigrantID; i++ {
				a.migrantAdmittedMonth[migrantIDHighBit|i] = m.LastAdvancedMonth
			}
		}

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
