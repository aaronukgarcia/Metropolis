package compose

import (
	"encoding/json"
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// FEAT-1972079943 — the composition root's OWN save.Participant, closing the
// last gap in the full-composed StateDigest STATE SNAPSHOT round-trip.
//
// # Snapshot semantics — read this first
//
// Composition.Save/Load is a STATE SNAPSHOT, NOT a tick-continuous resume. It
// restores every module's state plus these compose-owned ledgers such that
// loaded.StateDigest() == original.StateDigest() AT THE LOAD POINT. It does
// NOT restore the engine clock: a freshly Loaded composition is at tick 0
// even if the save was taken at tick 90. Restoring the clock touches the
// sealed-clock invariant and is deferred to FEAT-1972079944; for the
// snapshot+journal-tail restore path (FEAT-1972079936 inc3) the tail-replay
// re-establishes the clock, and for a standalone resume FEAT-1972079944 is
// required. This participant therefore serializes ONLY the digest-relevant
// durable state — never the once-per-tick ledger-closing trackers, which are
// clock-relative and whose restoration onto a tick-0 engine would be actively
// harmful (see the durable-vs-derived analysis below).
//
// # Why the composition root needs its own participant
//
// StateDigest() (state_digest.go, item 5) hashes a set of plain simState
// fields — the conservation / liveness ledgers — that NO per-module
// participant covers, because they are accumulated by the composition root's
// own phase hooks, not by any single engine module. Before this participant,
// a Save→Load of the seven module participants restored every module's state
// byte-for-byte but left these compose-owned ledgers at their fresh
// post-Wire defaults, so loaded.StateDigest() != original.StateDigest(). This
// participant serializes exactly the durable subset, and Composition.Load
// recomputes the derived subset from the restored modules (see the
// durable-vs-derived analysis below), so the FULL digest now round-trips at
// the load point.
//
// # Durable-vs-derived analysis (per field, verified against compose.go)
//
//	DERIVED / MIRROR — NOT serialized, RECOMPUTED on load:
//	  - treasury:      a publish-mirror of finance AcctTreasury. Seeded from
//	                   the ledger (compose.go ~L864) and re-synced from it by
//	                   syncMoneyFromLedger() on EVERY financeHook tick (the
//	                   LAST monthly phase) and after every demolish-compensation
//	                   post (BUG-324/BUG-355). At any StateDigest observation
//	                   point (state_digest.go: "called after the run's phase
//	                   pipeline has joined") treasury == AcctTreasury, so it is
//	                   reconstructable from the now-restored finance module.
//	  - citizenWealth: identical argument for finance AcctHouseholds.
//	  Both are recomputed by Composition.Load calling st.syncMoneyFromLedger()
//	  AFTER the finance participant has restored the ledger — order-safe
//	  because syncMoneyFromLedger reads only the finance module, which Load has
//	  already restored. (Honest caveat: a single command-handler code path — a
//	  demolish whose finance Post is REJECTED, compose.go ~L2125 — writes the
//	  treasury mirror WITHOUT a ledger post, transiently diverging the mirror
//	  from the ledger; but the very next financeHook tick's syncMoneyFromLedger
//	  clobbers that adjustment regardless of save/load, so at no legitimate
//	  digest observation point does treasury differ from the ledger. Recompute
//	  is therefore faithful for the digest's contract.)
//
//	DURABLE / compose-accumulated — SERIALIZED here (reconstructable from no
//	module — a running total or an opening baseline the composition root alone
//	holds):
//	  - moneyFlows:           cumulative gross money moved (AC-9). A tally, not
//	                          a live balance; no module holds it.
//	  - netMigration:         cumulative net migration applied (attract hook
//	                          folds res.NetApplied each month). A run-long tally.
//	  - consumptionDelivered: cumulative delivered utility quantity. A tally.
//	  - vitalBirths/vitalDeaths: cumulative real fertility/mortality folded into
//	                          peopleDelta so far (FEAT-169 liveness evidence).
//	                          Compose accumulates these one day-tick at a time
//	                          from AdvanceDayTick's return values; citizens holds
//	                          the live population, not the run-long birth/death
//	                          tallies, so these are not module-reconstructable.
//	  - peopleOpening/peopleDelta, moneyOpening/moneyDelta: the two conservation
//	                          ledgers' opening baseline + in-period delta. The
//	                          openings are captured at genesis / carried from the
//	                          previous tick's close; the deltas accumulate within
//	                          the current tick. Neither is a module observable.
//
//	NOT SERIALIZED — the BUG-288 once-per-tick ledger-closing trackers
//	(previousClosingPop, previousClosingMoney, lastClosedTick) are DELIBERATELY
//	omitted. They are NOT hashed by StateDigest, and they are clock-relative:
//	closeLedgerForTick gates on lastClosedTick vs the engine clock. Because
//	Load restores STATE but NOT the clock (a loaded composition is at tick 0 —
//	FEAT-1972079944), reinstating lastClosedTick=89 onto a tick-0 engine would
//	freeze closeLedgerForTick for 89 ticks — latent corruption. For a snapshot
//	they belong to the resume path (FEAT-1972079944, which restores the clock)
//	or are re-established by the journal-tail replay (FEAT-1972079936 inc3), not
//	to this state snapshot. The digest matches at the load point without them.
//
// SaveParticipant is satisfied STRUCTURALLY: this file imports only
// foundation/serialize (Record/RecordSource/RecordHandler), never
// internal/engine/save — the same discipline every per-module participant
// follows. NOTE (GR#25): this is the composition root's FIRST direct import of
// int.serializer; the edge feat.compositionroot → int.serializer is not yet
// registered in code.json and must be added by the Architect before this can
// land. Flagged, not self-registered.

const (
	// kindComposeLedger is this participant's stable shard label — unique
	// across the composition's participant list. save.Load routes the
	// matching shard's records back here by this Kind.
	kindComposeLedger = "compose"

	// recComposeLedger is the single record kind this participant emits.
	recComposeLedger = "compose.ledger"
)

// composeLedgerWire is the compose-owned conservation/liveness ledger on the
// wire. Every field carries an explicit json tag; the durable simState fields
// are projected into this struct (never marshalled off simState directly) so
// the wire shape is stable and independent of simState's internal layout.
type composeLedgerWire struct {
	MoneyFlows           int64   `json:"moneyFlows"`
	NetMigration         int64   `json:"netMigration"`
	ConsumptionDelivered float64 `json:"consumptionDelivered"`
	VitalBirths          int64   `json:"vitalBirths"`
	VitalDeaths          int64   `json:"vitalDeaths"`
	PeopleOpening        int64   `json:"peopleOpening"`
	PeopleDelta          int64   `json:"peopleDelta"`
	MoneyOpening         int64   `json:"moneyOpening"`
	MoneyDelta           int64   `json:"moneyDelta"`
}

// composeLedgerToWire projects the durable ledger fields off the live
// simState. treasury/citizenWealth are intentionally absent — they are
// recomputed from the finance module on load (see the file doc comment).
func composeLedgerToWire(st *simState) composeLedgerWire {
	return composeLedgerWire{
		MoneyFlows:           st.moneyFlows,
		NetMigration:         st.netMigration,
		ConsumptionDelivered: st.consumptionDelivered,
		VitalBirths:          st.vitalBirths,
		VitalDeaths:          st.vitalDeaths,
		PeopleOpening:        st.peopleOpening,
		PeopleDelta:          st.peopleDelta,
		MoneyOpening:         st.moneyOpening,
		MoneyDelta:           st.moneyDelta,
	}
}

// composeLedgerFromWire installs a decoded ledger wire back onto the live
// simState. treasury/citizenWealth are left untouched here — Composition.Load
// recomputes them from the restored finance ledger after the whole bundle has
// loaded.
func composeLedgerFromWire(st *simState, w composeLedgerWire) {
	st.moneyFlows = w.MoneyFlows
	st.netMigration = w.NetMigration
	st.consumptionDelivered = w.ConsumptionDelivered
	st.vitalBirths = w.VitalBirths
	st.vitalDeaths = w.VitalDeaths
	st.peopleOpening = w.PeopleOpening
	st.peopleDelta = w.PeopleDelta
	st.moneyOpening = w.MoneyOpening
	st.moneyDelta = w.MoneyDelta
}

// composeLedgerParticipant adapts the composition root's own simState ledgers
// to the save.Participant contract (Kind/Source/Handler), satisfied
// structurally via foundation/serialize. It wraps the live *simState the
// composition already owns; Source snapshots the durable ledger fields on
// save and Handler reinstalls them on load.
type composeLedgerParticipant struct {
	st *simState
}

// newComposeLedgerParticipant returns the compose-owned ledger participant
// over st. st is the composition's live simState (never a copy — simState is
// always held by pointer within the package), so no SEC-020 copy-guard is
// needed here.
func newComposeLedgerParticipant(st *simState) *composeLedgerParticipant {
	return &composeLedgerParticipant{st: st}
}

// Kind returns the compose ledger shard label.
func (p *composeLedgerParticipant) Kind() string { return kindComposeLedger }

// Source returns a fresh single-record pull-iterator over the durable ledger
// fields. It snapshots the fields into the wire struct once, up front (Save is
// called outside the tick, after the phase pipeline has joined — the same
// single-goroutine observation point StateDigest documents), then yields one
// record.
func (p *composeLedgerParticipant) Source() serialize.RecordSource {
	wire := composeLedgerToWire(p.st)
	emitted := false
	return func() (serialize.Record, bool, error) {
		if emitted {
			return serialize.Record{}, false, nil
		}
		data, err := json.Marshal(wire)
		if err != nil {
			return serialize.Record{}, false, fmt.Errorf("compose: marshalling %s record: %w", recComposeLedger, err)
		}
		emitted = true
		return serialize.Record{Kind: recComposeLedger, Data: data}, true, nil
	}
}

// Handler returns a fresh sink that reinstalls the durable ledger fields from
// the streamed record. Exactly one record is expected; an unknown kind fails
// loud and closed rather than silently loading a partial ledger.
func (p *composeLedgerParticipant) Handler() serialize.RecordHandler {
	return func(rec serialize.Record) error {
		if rec.Kind != recComposeLedger {
			return fmt.Errorf("compose: unknown compose-ledger save record kind %q", rec.Kind)
		}
		var w composeLedgerWire
		if err := json.Unmarshal(rec.Data, &w); err != nil {
			return fmt.Errorf("compose: decoding %s record: %w", rec.Kind, err)
		}
		composeLedgerFromWire(p.st, w)
		return nil
	}
}
