package compose

import (
	"github.com/aaronukgarcia/Metropolis/internal/engine/build"
	"github.com/aaronukgarcia/Metropolis/internal/engine/citizens"
	"github.com/aaronukgarcia/Metropolis/internal/engine/crime"
	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
	"github.com/aaronukgarcia/Metropolis/internal/engine/refuse"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/engine/traffic"
	"github.com/aaronukgarcia/Metropolis/internal/engine/unlocks"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
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
