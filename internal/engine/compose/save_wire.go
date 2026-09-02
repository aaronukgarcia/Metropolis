package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
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
// world, citizens, finance, build, refuse, traffic, unlocks, crime, market,
// consumption, attract, then the composition root's OWN ledger participant.
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
		// FEAT-1972079947 — engine.attract's own internal momentum state
		// (reputation/lastAdvancedMonth/nextMigrantID) closes the gap
		// TestLoadAt_KnownLimitation_AttractStateNotRestoredAcrossMonthBoundary
		// (save_loadat_test.go) named and proved: without this participant,
		// LoadAt's tick continuity broke the instant a NEW calendar month's
		// ApplyMigration ran, because attract's own momentum/id-counter state
		// was silently left at its post-Wire fresh default rather than
		// restored. attract itself is already a registered outbound edge of
		// this composition root (compose.go imports it directly).
		attract.NewSaveParticipant(st.attract),
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
// BUG-479: the bundle's header WorldSeed is compared against this
// composition's own seed (c.state.seed) BEFORE any participant state is
// applied, refusing with save.ErrSaveSeedMismatch on a mismatch — every
// module Participant's saved shard omits its own `seed` field on the
// claim "reproduced from save.Context.WorldSeed" (see Participants' doc
// comment), and until this check existed nothing enforced that claim.
// opts forwards to save.Manager.Load; pass save.AllowSeedMismatch() for
// a deliberate reseed (e.g. the FEAT-1972079897 rules-change replay
// case). The seed check can never be skipped by OMISSION: Load always
// PREPENDS save.WithExpectedWorldSeed(c.state.seed), so a caller that
// passes no opts (every in-tree caller today, incl. snapshot.go's
// RestoreLatestSnapshotOrGenesis) always gets the check. It CAN be
// overridden deliberately, because opts are applied after the prepended
// one and resolveLoadOptions is last-write-wins: a caller passing its
// own save.WithExpectedWorldSeed(...) replaces the composition seed as
// the expected value, exactly as save.AllowSeedMismatch() waives the
// refusal. Both are explicit Go-API opt-outs, neither is reachable
// without the caller naming an option (BUG-479 r1/r2 rounds; pinned by
// attack_bug479_optsoverride_test.go).
func (c *Composition) Load(root string, opts ...save.LoadOption) error {
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
	loadOpts := append([]save.LoadOption{save.WithExpectedWorldSeed(int64(c.state.seed))}, opts...)
	mgr := save.NewManager(root, c.Participants(), c.state.cid)
	if _, _, err := mgr.Load(dir, loadOpts...); err != nil {
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
	// FEAT-build-services-bridge-2026-09-02 round remedy (b): engine.services
	// is NOT itself a save.Participant above (it carries no durable state of
	// its OWN worth persisting independently — its instance table is fully
	// DERIVED from completed build orders), so a restore brings build's
	// queue back with already-complete service orders but an empty services
	// instance table. Re-drive registration for those orders here, right
	// after both build (Participants(), above) and services (constructed
	// once at Wire time and never rebuilt by Load) are in their
	// post-restore state — this is the single choke point every restore
	// path funnels through: bare Load, LoadAt, restoreFromSnapshotBytes
	// (snapshot.go's walk-back branch), and therefore
	// RestoreLatestSnapshotOrGenesis's snapshot branch, which is exactly
	// cmd/metroserve's durable-host startup path (persist.go's
	// wireAndRehydrate -> RestoreLatestSnapshotOrGenesis). The genesis-replay
	// fallback (restoreGenesis, snapshot.go) does NOT go through Load at
	// all — it re-executes the journaled SubmitBuildCommand/Tick commands
	// from tick 0, which re-derives every registration naturally through
	// Tick's own self-healing sweep (build.go's
	// registerCompletedServicesLocked, now idempotent per remedy (a)), so
	// calling it again here would be a harmless no-op for that path, not a
	// gap. BuildAPI.RegisterCompletedServices is itself idempotent (a no-op
	// over any order already tracked), so calling it unconditionally on
	// every Load — not just a snapshot restore — is safe.
	if err := c.state.buildAPI.RegisterCompletedServices(); err != nil {
		return errs.Wrap(ErrModuleFailed, c.state.cid, err, map[string]any{"module": "build", "step": "register-completed-services"})
	}
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
//
// BUG-479: LoadAt delegates straight to Load for the state restore, so it
// inherits Load's WorldSeed-vs-composition-seed check unconditionally
// (refusing, before the clock is ever seeded, on a mismatch) — opts
// forwards to that Load call exactly as Load's own opts parameter does;
// pass save.AllowSeedMismatch() here for a deliberate reseed.
func (c *Composition) LoadAt(root string, tick int64, opts ...save.LoadOption) error {
	if err := c.Load(root, opts...); err != nil {
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
