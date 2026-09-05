package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/attract"
	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/consumption"
	"github.com/aaronukgarcia/Metropolis/internal/engine/crime"
	"github.com/aaronukgarcia/Metropolis/internal/engine/deathservices"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/market"
	"github.com/aaronukgarcia/Metropolis/internal/engine/refuse"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/engine/services"
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
//
// FEAT-build-services-bridge-2026-09-02 round remedy (root fix): engine.
// services (st.services) is now ALSO a save.Participant
// (services.NewSaveParticipant) — the composition root already has a
// registered edge to engine.services (compose.go imports it directly for
// the SetFunding command seam), so this addition needs no NEW
// compositionroot-level edge. The one genuinely new edge — engine.services'
// OWN outbound edge to int.serializer (foundation/serialize) — was
// registered by the lead in master-plan-v2.1.json/code.json (commit
// 73696fd, GR#25, independently verified) before this file landed.

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
// consumption, attract, services, then the composition root's OWN ledger
// participant.
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
		// FEAT-build-services-bridge-2026-09-02 round remedy (root fix):
		// engine.services' own registered instance table (spec, funding,
		// currentUpgrade, demand/demandDist, allocated, districtDemand,
		// poolAvailable) round-trips through this participant, closing the
		// live-composition-rewind phantom-instance hazard the round found —
		// see services/participant.go's doc comment for the full
		// durable-vs-derived analysis and the BUG-586 reconciliation rule.
		services.NewSaveParticipant(st.services),
		// BUG-689: engine.deathservices' own bodies/plots/budgets/
		// dispensation/handoff-cursor state (participant.go's doc comment
		// has the full durable-field analysis). Closes the AC-20 round's
		// second built-but-not-wired half — without this, a save/restore
		// boundary would silently drop every body record AND the handoff
		// cursor, double-delivering the whole handoff stream on the next
		// restore once the monthly Intake hook (compose.go's
		// deathServicesHook) is live.
		deathservices.NewSaveParticipant(st.deathServices),
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
	header, _, err := mgr.Load(dir, loadOpts...)
	if err != nil {
		// A refused load (seed mismatch, corrupt bundle, ...) must leave
		// every participant untouched (TestBUG479_Load_SeedMismatch_...'s
		// pristine-digest proof) — so the F1 reset below runs ONLY after
		// mgr.Load has actually succeeded, never on this path.
		return errs.Wrap(ErrModuleFailed, c.state.cid, err, map[string]any{"module": "save", "step": "load", "dir": dir})
	}
	// BUG-689 round follow-up F1: save.Manager.Load only calls a
	// Participant's Handler for shards its bundle header actually lists
	// (load.go's shard loop ranges over header.ShardIndex, never the
	// registered participant set) — a bundle with NO deathservices shard
	// (every pre-BUG-689 save) therefore never touches deathservices at
	// all, leaving its live state exactly as it was before this Load while
	// every OTHER module IS reset and restored just above. Detected here
	// from the now-validated header (checked ONLY after a successful
	// mgr.Load, so a refused load never mutates deathservices either) and
	// reset explicitly so "no shard present" always means "clean module
	// state" regardless of the bundle's contents. When the shard IS
	// present this is a deliberate no-op: Handler already reset the module
	// eagerly on construction (participant.go's Handler doc) before
	// installing the streamed records.
	hasDeathServicesShard := false
	for _, sm := range header.ShardIndex {
		if sm.Kind == deathservices.KindDeathServices {
			hasDeathServicesShard = true
			break
		}
	}
	if !hasDeathServicesShard {
		if err := c.state.deathServices.ResetForLoad(c.state.cid); err != nil {
			return errs.Wrap(ErrModuleFailed, c.state.cid, err, map[string]any{"module": "deathservices", "step": "reset-for-load"})
		}
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
	// FEAT-build-services-bridge-2026-09-02 root fix + round remedy (b):
	// engine.services IS now a save.Participant (Participants(), above) — its
	// own instance table, funding/upgrade/demand/allocated state,
	// districtDemand, and poolAvailable round-trip verbatim, closing the
	// live-composition-rewind phantom-instance hazard the round found (a
	// service instance that existed only in a LATER save/live-composition
	// state no longer survives a Load to an earlier one: resetForLoad wipes
	// the instance table before the loaded records repopulate it — see
	// services/participant.go's doc comment).
	//
	// RegisterCompletedServices still runs here, unconditionally, on every
	// Load — NOT only to cover engine.services (which now restores its own
	// state), but because it remains the ONLY rebuild path for an OLD save
	// taken before this participant existed (no "services.*" shard in the
	// bundle ⇒ services.SaveParticipant.Handler never runs ⇒ the live
	// ServicesAPI is left exactly as SetServices/New constructed it, empty)
	// — the documented migration path this round's fix must not regress.
	// registerServiceLocked (build/build.go) already treats
	// services.ErrDuplicateService as a no-op success (round remedy (a)),
	// so calling the sweep AFTER this participant has restored a service
	// the sweep also wants to (re-)register is a harmless idempotent no-op:
	// the RESTORED funding/currentUpgrade/demand/allocated state is never
	// overwritten by the sweep's fresh-spec re-derivation, because
	// RegisterService only ever INSERTS a new id — it never mutates an
	// existing one. Reconciliation rule when the two disagree: BUILD's own
	// restored state (b.structures — the authoritative "still standing"
	// record, itself an exact save round-trip) decides which build-order-
	// derived services exist going forward; a services instance this
	// participant restores for an order the loaded build queue no longer
	// marks complete+standing is a residual the sweep does not clean up
	// (the sweep is additive-only) — this is a known, documented gap
	// (services/participant.go's doc comment), tracked as a follow-up for a
	// dedicated cross-participant reconciliation pass, since normal Load
	// calls always restore build and services from the SAME save bundle (so
	// the two are mutually consistent at every real save point; the gap is
	// reachable only from a hand-corrupted or externally-crafted save).
	// This is the single choke point every restore path funnels through:
	// bare Load, LoadAt, restoreFromSnapshotBytes (snapshot.go's walk-back
	// branch), and therefore RestoreLatestSnapshotOrGenesis's snapshot
	// branch, which is exactly cmd/metroserve's durable-host startup path
	// (persist.go's wireAndRehydrate -> RestoreLatestSnapshotOrGenesis). The
	// genesis-replay fallback (restoreGenesis, snapshot.go) does NOT go
	// through Load at all — it re-executes the journaled
	// SubmitBuildCommand/Tick commands from tick 0, which re-derives every
	// registration naturally through Tick's own self-healing sweep
	// (build.go's registerCompletedServicesLocked, idempotent per remedy
	// (a)), so calling RegisterCompletedServices again here is a harmless
	// no-op for that path, not a gap.
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
