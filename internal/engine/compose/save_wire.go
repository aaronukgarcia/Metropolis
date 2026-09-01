package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/consumption"
	"github.com/aaronukgarcia/Metropolis/internal/engine/crime"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/refuse"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/engine/traffic"
	"github.com/aaronukgarcia/Metropolis/internal/engine/unlocks"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/num"
)

// FEAT-1972079941 AC-6 — save live-wiring. This is the integration payoff:
// the composition root is the correct assembler of the per-module
// save.Participant list, because it already OWNS every module instance
// (simState). save.DefaultParticipants is deliberately left empty (a
// literal would force the save package to import every engine module — a
// layering inversion); the composition root, which already imports them
// all, is where the concrete instances and the Participant contract meet.
//
// New GR#25 outbound edges this file introduces (feat.compositionroot →):
//   - feat.saveux (internal/engine/save) — save.Manager/Participant/Context
//   - engine.unlocks (internal/engine/unlocks) — its NewSaveParticipant
//
// Both must be registered in code.json before this can land (astgate's
// live-tree scan enforces the import graph). The other five participants'
// packages (world/citizens/finance/build/refuse/traffic) are already on
// the composition root's registered outbound edge set.

// compositionSaveName is the fixed manual-save display name Save writes and
// Load reads back. A stable, filesystem-safe literal (SaveManual rejects
// unsafe names): the composition writes one well-known slot, so Save over
// the same root overwrites it and Load finds it unambiguously.
const compositionSaveName = "composition"

// compositionSaveAppVersion is the build-identity string stamped into the
// save header's AppVersion field. The save package does not import
// foundation/buildinfo (see save.Context's doc comment), and this field is
// free-form header metadata that never affects module round-trip, so the
// composition stamps a fixed label rather than threading buildinfo through
// every Save call.
const compositionSaveAppVersion = "metropolis-compose"

// Participants assembles the composition's per-module save.Participant list
// in a STABLE, documented order (never a map range — GR#21). Each entry is
// built from the live module instance this composition already owns, via
// that module's own NewSaveParticipant constructor (which satisfies
// save.Participant structurally — no module imports engine/save).
//
// Order is fixed and load-order-independent (save routes each shard back to
// its Participant by Kind, and a fresh RecordSource is taken per Source
// call), but stated explicitly so the saved shard sequence is deterministic:
// world, citizens, finance, build, refuse, traffic, unlocks, crime, then the
// composition root's OWN ledger participant.
//
// FEAT-1972079943 completed the full-composed StateDigest STATE SNAPSHOT
// round-trip by adding the last two entries:
//   - crime (crime.NewSaveParticipant, the 8th module participant): crime
//     observables are part of StateDigest but were saved by nothing.
//   - the compose-owned conservation/liveness ledgers
//     (newComposeLedgerParticipant, Kind "compose"): the durable simState
//     fields StateDigest hashes that no module holds — see
//     compose_ledger_participant.go's durable-vs-derived analysis. The
//     derived mirrors (treasury/citizenWealth) are NOT in that participant;
//     Load recomputes them from the restored finance ledger.
//
// SNAPSHOT, NOT tick-continuous: Save/Load restore module + ledger STATE so
// StateDigest() matches AT THE LOAD POINT, but do NOT restore the engine clock
// (a loaded composition is at tick 0). Clock restoration is FEAT-1972079944
// (it touches the sealed-clock invariant); the snapshot+journal-tail restore
// path (FEAT-1972079936 inc3) re-establishes the clock via tail-replay.
func (c *Composition) Participants() []save.Participant {
	st := c.state
	return []save.Participant{
		world.NewSaveParticipant(st.world),
		citizens.NewSaveParticipant(st.citizens),
		finance.NewSaveParticipant(st.finance),
		build.NewSaveParticipant(st.buildAPI),
		refuse.NewSaveParticipant(st.refuse),
		traffic.NewSaveParticipant(st.traffic),
		unlocks.NewSaveParticipant(st.unlocks),
		crime.NewSaveParticipant(st.crime),
		// FEAT-1972079945 (Aaron 2026-09-01): market + consumption are STATELESS
		// today, but each ships an empty-but-conformant participant so the
		// save-bundle shape is uniform across every composed module and any
		// future mutable field is forced through the field-parity drift test
		// at birth rather than silently unserialized.
		market.NewSaveParticipant(st.market),
		consumption.NewSaveParticipant(st.consumption),
		newComposeLedgerParticipant(st),
	}
}

// Save writes a full manual save of every composed module's state under
// root, via a save.Manager built over Participants(). It is the composed
// engine's single save entry point (mirrors Wire's single-wiring-path
// discipline). The save.Context is derived entirely from the engine's own
// deterministic clock (world seed, current tick, current month) — never a
// wall clock (save AC-15).
func (c *Composition) Save(root string) error {
	clock, err := c.state.e.Clock()
	if err != nil {
		return errs.Wrap(ErrModuleFailed, c.state.cid, err, map[string]any{"module": "save", "step": "clock"})
	}
	ctx := save.Context{
		WorldSeed:     int64(c.state.seed),
		CreatedAtTick: clock.Tick(),
		GameMonth:     clock.Month(),
		AppVersion:    compositionSaveAppVersion,
	}
	mgr := save.NewManager(root, c.Participants(), c.state.cid)
	return mgr.SaveManual(ctx, compositionSaveName)
}

// Load reconstructs every composed module's state from the manual save
// written by Save under root. It builds a save.Manager over THIS
// composition's Participants() (so each shard streams straight back into
// this composition's live module instances via their Handler) and loads the
// well-known composition bundle. A fresh Composition therefore becomes a
// STATE-EXACT replica of the saved one for every module that implements the
// Participant contract: its StateDigest() matches AT the load point. This is a
// snapshot, not a tick-continuous resume — Load does NOT restore the engine
// clock (a loaded composition is at tick 0), so continuing to tick a loaded
// composition is NOT equivalent to continuing the original. Clock restoration
// is FEAT-1972079944; the journal-tail replay (FEAT-1972079936 inc3) supplies
// the clock/continuation on top of this state snapshot.
func (c *Composition) Load(root string) error {
	summaries, _, err := save.List(root)
	if err != nil {
		return errs.Wrap(ErrModuleFailed, c.state.cid, err, map[string]any{"module": "save", "step": "list"})
	}
	dir := ""
	for _, s := range summaries {
		if s.DisplayName == compositionSaveName {
			dir = s.Path
			break
		}
	}
	if dir == "" {
		return errs.New(ErrModuleFailed, c.state.cid, map[string]any{
			"module": "save",
			"step":   "locate",
			"name":   compositionSaveName,
			"root":   root,
			"cause":  "no composition save named " + compositionSaveName + " found under root",
		})
	}
	mgr := save.NewManager(root, c.Participants(), c.state.cid)
	if _, _, err := mgr.Load(dir); err != nil {
		return errs.Wrap(ErrModuleFailed, c.state.cid, err, map[string]any{"module": "save", "step": "load", "dir": dir})
	}
	// FEAT-1972079943 — recompute the DERIVED compose-owned ledgers from the
	// now-restored modules. treasury/citizenWealth are publish-mirrors of the
	// finance ledger (AcctTreasury / AcctHouseholds); the compose ledger
	// participant deliberately does NOT serialize them, so re-sync them here,
	// after mgr.Load has restored the finance participant. Order-safe:
	// syncMoneyFromLedger reads only the finance module, which the load above
	// has already reconstructed. With the durable ledgers restored by the
	// compose participant and these two mirrors recomputed, the full
	// StateDigest round-trips exactly.
	c.state.syncMoneyFromLedger()
	return nil
}

// LoadAt is Load's tick-continuous sibling (FEAT-1972079944, Aaron's ruling
// option A): it restores every composed module's state exactly like Load
// does, seeds the engine's clock to tick via the narrow, restore-only
// core.Engine.SeedClockForRestore, and re-establishes the BUG-288
// ledger-closing trackers (compose_ledger_participant.go's "NOT SERIALIZED"
// section -- lastClosedTick/previousClosingPop/previousClosingMoney) that a
// plain Load deliberately leaves at their fresh, clock-relative defaults --
// so the returned composition is not just state-exact but genuinely
// tick-continuous with the original: driving it forward with AdvanceTicks
// from here produces the same state a same-seed engine that never stopped
// would have produced.
//
// Why the trackers need re-establishing, not just the clock: snapshot()'s
// closeLedgerForTick gates purely on lastClosedTick vs the current tick
// (compose.go). A fresh Wire's lastClosedTick is 0, so the very next
// invariant-hook tick after a clock-seeded-but-tracker-naive load would see
// lastClosedTick(0) < tick+1 and roll the ledger's opening baseline back to
// previousClosingPop/Money's fresh-Wire zero values -- corrupting the
// conservation ledger even though every module's own state loaded exactly
// right. Because Load has already restored citizens/finance byte-exact (and
// syncMoneyFromLedger has already re-synced the treasury/citizenWealth
// mirrors), the closing values a live engine would have recorded at the END
// of tick `tick` are exactly the CURRENT restored population and money
// totals -- so LoadAt sets lastClosedTick=tick and
// previousClosingPop/Money from those already-restored figures, matching
// what closeLedgerForTick(tick) itself would have left behind.
//
// This is deliberately a SEPARATE entry point rather than a change to
// Load's own behaviour: Load's documented contract (a state snapshot at
// tick 0) is unchanged -- see TestSaveRoundTrip_IsSnapshotNotTickContinuous
// -- and the sealed-clock invariant (a live/already-ticked engine can never
// have its clock set except by AdvanceTicks) is preserved unconditionally,
// because SeedClockForRestore itself refuses to run once the engine has
// sealed. LoadAt only succeeds, therefore, on a freshly constructed,
// never-yet-ticked Engine -- exactly the shape a caller reconstructing a
// composition for a Load has (see buildComposition in
// save_roundtrip_test.go): a bare core.NewEngine + Wire, before any command
// or AdvanceTicks call has touched it.
//
// tick is normally the CreatedAtTick the corresponding Save call recorded
// (see Save's save.Context construction above) -- callers restoring from a
// snapshot+journal-tail bundle (FEAT-1972079936 inc3) pass that snapshot's
// tick here before replaying the tail.
func (c *Composition) LoadAt(root string, tick int64) error {
	if err := c.Load(root); err != nil {
		return err
	}
	if err := c.state.e.SeedClockForRestore(c.state.cid, tick); err != nil {
		return errs.Wrap(ErrModuleFailed, c.state.cid, err, map[string]any{"module": "save", "step": "seed-clock", "tick": tick})
	}
	// Re-establish the BUG-288 clock-relative ledger-closing trackers a
	// plain Load deliberately leaves unrestored (see this method's doc
	// comment): the closing values a live engine would hold immediately
	// after tick `tick`'s snapshot are exactly the population/money totals
	// Load has already restored (state is byte-exact at this point).
	st := c.state
	st.lastClosedTick = tick
	st.previousClosingPop = int64(st.citizens.TotalPopulation(st.cid))
	st.previousClosingMoney = num.SatAdd(st.treasury, st.citizenWealth)
	return nil
}
