package compose

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079936 Phase 1 inc3 — durable snapshot cadence + snapshot-aware
// restore. These tests prove: (1) ShouldSnapshot's cadence gate is exact and
// deterministic (no wall-clock, GR#21), (2) MaybeSnapshot only ever writes
// at cadence boundaries and its bounded retention (MaxRetainedSnapshots)
// actually deletes the oldest snapshots, (3) the headline payoff —
// snapshot-aware restore (latest snapshot + journal-tail replay) reproduces
// EXACTLY the same StateDigest and clock tick as a straight-through
// reference run, and also exactly matches the pre-existing genesis-replay
// path, and (4) the digest-equality comparisons above have real teeth: a
// deliberately truncated tail replay is proven to diverge, and
// splitJournalAtTick's own boundary arithmetic is proven correct
// (exact-boundary, mid-command straddling, and out-of-sync-journal error
// cases).

// advanceViaCommand submits an AdvanceTicks command through e.HandleCommand
// (NOT e.AdvanceTicks directly) so the advance is durably journaled —
// core.Engine.AdvanceTicks itself does not journal (see
// core/engine.go:AdvanceTicks); only the command path
// (handleAdvanceTicks -> accept -> journalAccepted) does. Snapshot-aware
// restore's journal-tail replay depends on every tick-advancing step being
// a journaled AdvanceTicks command, so every test in this file must drive
// ticks this way, not via driveMultiDomain's direct e.AdvanceTicks call.
func advanceViaCommand(t *testing.T, e *core.Engine, n int64) {
	t.Helper()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: n},
	}
	res := e.HandleCommand(cmd)
	if !res.Accepted {
		t.Fatalf("AdvanceTicks(%d) command rejected: %+v", n, res.Error)
	}
}

// advanceViaCommandExpectHalt drives ONE AdvanceTicks{N:n} command that this
// test's fault-injecting store is deliberately about to fail the durable
// append for -- BUG-472's "HALT + SURFACE" ruling (Aaron, 2026-09-01,
// superseding the original swallow-and-continue policy every BUG-480 test in
// this file was originally written against) means this specific command's
// own tick effect still applies (journalAccepted's doc comment,
// internal/engine/core/commands.go, explains why that ordering is
// unavoidable) but the wire CommandResult is REJECTED with
// core.ErrSimulationPersistHalted, and the Engine is now permanently halted:
// no FURTHER command of any kind will ever be accepted on e again. Callers
// use this exactly once per Engine, for the one command whose append is
// engineered to fail.
func advanceViaCommandExpectHalt(t *testing.T, e *core.Engine, n int64) {
	t.Helper()
	cmd := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.NewCorrelationID(),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: n},
	}
	res := e.HandleCommand(cmd)
	if res.Accepted {
		t.Fatalf("AdvanceTicks(%d) command accepted, want rejected (BUG-472 halt policy) -- the fault-injecting store did not fail this call as expected", n)
	}
	if res.Error == nil || res.Error.Code != core.ErrSimulationPersistHalted {
		t.Fatalf("AdvanceTicks(%d) rejection = %+v, want code %s", n, res.Error, core.ErrSimulationPersistHalted)
	}
}

// buildPersistedComposition wires a fresh composition with durable
// persistence enabled (Deps.PersistStore/PersistCity), so every accepted
// command — gameplay AND AdvanceTicks — is durably journaled via
// persistCommandJournaler (inc2).
func buildPersistedComposition(t *testing.T, store persist.Store, city persist.CityKey) (*core.Engine, *Composition) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	comp, err := Wire(e, &Deps{PersistStore: store, PersistCity: city})
	if err != nil {
		t.Fatalf("Wire (persisted): %v", err)
	}
	return e, comp
}

// driveGameplayOnly issues the same Buy+Zone+Build opening driveMultiDomain
// uses (all journaled gameplay commands), WITHOUT any tick advance — tests
// in this file drive ticks themselves via advanceViaCommand so cadence
// boundaries land exactly where each test expects.
func driveGameplayOnly(t *testing.T, e *core.Engine) {
	t.Helper()
	cells := []protocol.CellRef{{X: 0, Y: 0}, {X: 1, Y: 0}, {X: 0, Y: 1}}
	buy := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("snap-buy"),
		Kind:            protocol.KindBuy,
		Payload:         protocol.BuyPayload{Cell: cells[0]},
	}
	if res := e.HandleCommand(buy); !res.Accepted {
		t.Fatalf("Buy rejected: %+v", res.Error)
	}
	for i, cell := range cells {
		zone := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID("snap-zone"),
			Kind:            protocol.KindZone,
			Payload:         protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"},
		}
		if res := e.HandleCommand(zone); !res.Accepted {
			t.Fatalf("Zone %d rejected: %+v", i, res.Error)
		}
		build := protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID("snap-build"),
			Kind:            protocol.KindBuild,
			Payload:         protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"},
		}
		if res := e.HandleCommand(build); !res.Accepted {
			t.Fatalf("Build %d rejected: %+v", i, res.Error)
		}
	}
}

// TestShouldSnapshot_CadenceIsExactAndDeterministic proves the cadence gate
// fires ONLY at strictly-positive exact multiples of SnapshotCadenceTicks —
// a pure function of tick, never wall-clock (GR#21).
func TestShouldSnapshot_CadenceIsExactAndDeterministic(t *testing.T) {
	cases := []struct {
		tick int64
		want bool
	}{
		{0, false},
		{1, false},
		{SnapshotCadenceTicks - 1, false},
		{SnapshotCadenceTicks, true},
		{SnapshotCadenceTicks + 1, false},
		{2 * SnapshotCadenceTicks, true},
		{2*SnapshotCadenceTicks - 1, false},
		{3 * SnapshotCadenceTicks, true},
	}
	for _, tc := range cases {
		if got := ShouldSnapshot(tc.tick); got != tc.want {
			t.Errorf("ShouldSnapshot(%d) = %v, want %v", tc.tick, got, tc.want)
		}
	}
	if SnapshotCadenceTicks != 360 {
		t.Fatalf("SnapshotCadenceTicks = %d, want 360 (12*DailyTicksPerMonth=12*30, Aaron's 2026-08-31 ruling) — update this test's expectations if the placeholder was deliberately retuned", SnapshotCadenceTicks)
	}
}

// TestMaybeSnapshot_FiresOnlyAtCadenceBoundaries drives a composition tick
// by tick (the finest granularity, so no cadence boundary can ever be
// skipped) and proves a durable snapshot is written EXACTLY at each
// multiple of SnapshotCadenceTicks and at no other tick.
func TestMaybeSnapshot_FiresOnlyAtCadenceBoundaries(t *testing.T) {
	ctx := context.Background()
	store := persist.NewMemStore()
	city := persist.CityKey{TenantID: "t", CityID: "cadence"}
	e, comp := buildPersistedComposition(t, store, city)

	const totalTicks = int64(3)*SnapshotCadenceTicks + 5
	var snapshotsAtTick []int64
	for i := int64(0); i < totalTicks; i++ {
		advanceViaCommand(t, e, 1)
		_, ok, err := comp.MaybeSnapshot(ctx, store, city)
		if err != nil {
			t.Fatalf("MaybeSnapshot at tick %d: %v", i+1, err)
		}
		if ok {
			snapshotsAtTick = append(snapshotsAtTick, i+1)
		}
	}

	want := []int64{SnapshotCadenceTicks, 2 * SnapshotCadenceTicks, 3 * SnapshotCadenceTicks}
	if len(snapshotsAtTick) != len(want) {
		t.Fatalf("snapshots fired at %v, want %v", snapshotsAtTick, want)
	}
	for i := range want {
		if snapshotsAtTick[i] != want[i] {
			t.Fatalf("snapshots fired at %v, want %v", snapshotsAtTick, want)
		}
	}

	ids, err := store.ListSnapshots(ctx, city)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(ids) != len(want) {
		t.Fatalf("store holds %d snapshots, want %d (retention bound not yet exceeded)", len(ids), len(want))
	}
}

// TestMaybeSnapshot_PruningRetainsBound proves bounded snapshot retention:
// once more than MaxRetainedSnapshots cadence boundaries have been crossed,
// the store never holds more than MaxRetainedSnapshots snapshots for the
// city, and it is always the NEWEST ones that survive (the oldest are
// pruned first) — mirroring engine.checkpoint's MaxRetainedForks pattern.
// The journal itself (RestoreCommands) is proven UNAFFECTED by pruning —
// snapshots are a restore-speed optimization only, never a journal
// replacement (this increment's ruling).
func TestMaybeSnapshot_PruningRetainsBound(t *testing.T) {
	ctx := context.Background()
	store := persist.NewMemStore()
	city := persist.CityKey{TenantID: "t", CityID: "pruning"}
	e, comp := buildPersistedComposition(t, store, city)

	const cadenceCrossings = MaxRetainedSnapshots + 3
	for i := int64(0); i < cadenceCrossings; i++ {
		advanceViaCommand(t, e, SnapshotCadenceTicks)
		id, ok, err := comp.MaybeSnapshot(ctx, store, city)
		if err != nil {
			t.Fatalf("MaybeSnapshot (crossing %d): %v", i, err)
		}
		if !ok {
			t.Fatalf("MaybeSnapshot (crossing %d): expected a snapshot, got none (id=%q)", i, id)
		}
	}

	ids, err := store.ListSnapshots(ctx, city)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(ids) != MaxRetainedSnapshots {
		t.Fatalf("store holds %d snapshots after %d cadence crossings, want exactly MaxRetainedSnapshots=%d (pruning did not retain the bound)", len(ids), cadenceCrossings, MaxRetainedSnapshots)
	}

	// The journal itself must be completely unpruned: every crossing's
	// AdvanceTicks command is still durably present.
	cmds, err := RestoreCommands(ctx, store, city)
	if err != nil {
		t.Fatalf("RestoreCommands: %v", err)
	}
	var journaledTicks int64
	for _, cmd := range cmds {
		if cmd.Kind == protocol.KindAdvanceTicks {
			journaledTicks += cmd.Payload.(protocol.AdvanceTicksPayload).N
		}
	}
	if journaledTicks != cadenceCrossings*SnapshotCadenceTicks {
		t.Fatalf("journal retained %d ticks worth of AdvanceTicks commands, want %d — pruning must never touch the journal", journaledTicks, cadenceCrossings*SnapshotCadenceTicks)
	}
}

// TestSnapshotRestore_MatchesReferenceAndUsesTailReplay is the headline
// payoff test: a live composition is driven through gameplay commands, then
// two AdvanceTicks commands that land EXACTLY on a cadence boundary
// (160+200=360), triggering one durable snapshot, then two more AdvanceTicks
// commands AFTER the snapshot (10+10=20 more ticks, tick 380 final — kept
// well inside the SAME calendar month the snapshot was taken in, exactly
// like TestLoadAt_TickContinuity's own extraTicks does for LoadAt: crossing
// a NEW month after a restore hits the separately-filed, already-documented
// FEAT-1972079947 gap — engine.attract's own momentum state has no
// save.Participant — which is LoadAt's known limitation, not something
// this increment's snapshot/tail-replay plumbing introduces or is scoped to
// fix).
//
// A completely independent reference composition is driven through the
// IDENTICAL command sequence with no save/restore at all — the ground
// truth. RestoreLatestSnapshotOrGenesis on a THIRD, freshly Wired
// composition must reproduce the reference's StateDigest and clock tick
// exactly, using the snapshot (usedSnapshot=true) plus ONLY the
// post-snapshot journal tail — proving both that Save/LoadAt capture state
// correctly (FEAT-1972079941/1972079943/1972079944's payoff) and that
// splitJournalAtTick correctly excludes the pre-snapshot commands.
func TestSnapshotRestore_MatchesReferenceAndUsesTailReplay(t *testing.T) {
	ctx := context.Background()
	runSequence := func(e *core.Engine) {
		driveGameplayOnly(t, e)
		advanceViaCommand(t, e, 160)
		advanceViaCommand(t, e, 200) // tick=360 -- exact cadence boundary
		advanceViaCommand(t, e, 10)  // tick=370 (still month 12)
		advanceViaCommand(t, e, 10)  // tick=380 (still month 12)
	}

	// Reference: ground truth, no save/restore involved at all.
	eRef, compRef := buildComposition(t)
	runSequence(eRef)
	refDigest := compRef.StateDigest()
	refClock, err := eRef.Clock()
	if err != nil {
		t.Fatalf("Clock (reference): %v", err)
	}
	if refClock.Tick() != 380 {
		t.Fatalf("precondition: reference tick = %d, want 380", refClock.Tick())
	}

	// Live: same sequence, durably journaled + snapshotted at cadence.
	store := persist.NewMemStore()
	city := persist.CityKey{TenantID: "t", CityID: "restore-payoff"}
	eLive, compLive := buildPersistedComposition(t, store, city)
	driveGameplayOnly(t, eLive)
	advanceViaCommand(t, eLive, 160)
	advanceViaCommand(t, eLive, 200)
	if _, ok, err := compLive.MaybeSnapshot(ctx, store, city); err != nil {
		t.Fatalf("MaybeSnapshot: %v", err)
	} else if !ok {
		t.Fatal("MaybeSnapshot: expected a snapshot at tick 360, got none")
	}
	advanceViaCommand(t, eLive, 10)
	advanceViaCommand(t, eLive, 10)
	if compLive.StateDigest() != refDigest {
		t.Fatalf("precondition: live and reference digests differ before restore is even attempted")
	}

	ids, err := store.ListSnapshots(ctx, city)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("precondition: expected exactly 1 snapshot, got %d", len(ids))
	}

	// Restore: fresh, never-ticked composition, via snapshot + tail replay.
	eR := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	compR, err := Wire(eR, nil)
	if err != nil {
		t.Fatalf("Wire (restore target): %v", err)
	}
	usedSnapshot, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eR, compR, store, city)
	if err != nil {
		t.Fatalf("RestoreLatestSnapshotOrGenesis: %v", err)
	}
	if !usedSnapshot {
		t.Fatal("RestoreLatestSnapshotOrGenesis: usedSnapshot = false, want true (a snapshot exists)")
	}
	if tick != 380 {
		t.Fatalf("RestoreLatestSnapshotOrGenesis: tick = %d, want 380", tick)
	}
	if compR.StateDigest() != refDigest {
		t.Fatalf("snapshot-aware restore StateDigest mismatch: got %x, want %x (reference)", compR.StateDigest(), refDigest)
	}
	restoredClock, err := eR.Clock()
	if err != nil {
		t.Fatalf("Clock (restored): %v", err)
	}
	if restoredClock.Tick() != refClock.Tick() {
		t.Fatalf("restored clock tick = %d, want %d", restoredClock.Tick(), refClock.Tick())
	}
}

// TestSnapshotRestore_FallsBackToGenesisWhenNoSnapshotExists proves the
// no-snapshot-yet fallback: fewer ticks than one cadence period means the
// store holds zero snapshots, and restore must fall back to the
// pre-existing genesis-replay path (usedSnapshot=false) while still
// reproducing the reference exactly.
func TestSnapshotRestore_FallsBackToGenesisWhenNoSnapshotExists(t *testing.T) {
	ctx := context.Background()
	runSequence := func(e *core.Engine) {
		driveGameplayOnly(t, e)
		advanceViaCommand(t, e, 100) // well short of SnapshotCadenceTicks=360
	}

	eRef, compRef := buildComposition(t)
	runSequence(eRef)
	refDigest := compRef.StateDigest()

	store := persist.NewMemStore()
	city := persist.CityKey{TenantID: "t", CityID: "genesis-fallback"}
	eLive, compLive := buildPersistedComposition(t, store, city)
	runSequence(eLive)
	if _, ok, err := compLive.MaybeSnapshot(ctx, store, city); err != nil {
		t.Fatalf("MaybeSnapshot: %v", err)
	} else if ok {
		t.Fatal("MaybeSnapshot fired before reaching a cadence boundary")
	}

	if ids, err := store.ListSnapshots(ctx, city); err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	} else if len(ids) != 0 {
		t.Fatalf("precondition: expected zero snapshots, got %d", len(ids))
	}

	eR := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	compR, err := Wire(eR, nil)
	if err != nil {
		t.Fatalf("Wire (restore target): %v", err)
	}
	usedSnapshot, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eR, compR, store, city)
	if err != nil {
		t.Fatalf("RestoreLatestSnapshotOrGenesis: %v", err)
	}
	if usedSnapshot {
		t.Fatal("RestoreLatestSnapshotOrGenesis: usedSnapshot = true, want false (no snapshot exists — must fall back to genesis)")
	}
	if tick != 100 {
		t.Fatalf("tick = %d, want 100", tick)
	}
	if compR.StateDigest() != refDigest {
		t.Fatalf("genesis-fallback restore StateDigest mismatch: got %x, want %x", compR.StateDigest(), refDigest)
	}
}

// TestSnapshotRestore_ProveCanFail_TruncatedTailDiverges proves the
// digest-equality assertions above have real teeth: replaying only PART of
// the correct journal tail (dropping the final AdvanceTicks command) must
// NOT reproduce the reference's StateDigest. This is the negative control
// — if this test ever passed with digests equal, the comparison used by
// the payoff test above would be vacuous.
func TestSnapshotRestore_ProveCanFail_TruncatedTailDiverges(t *testing.T) {
	ctx := context.Background()

	eRef, compRef := buildComposition(t)
	driveGameplayOnly(t, eRef)
	advanceViaCommand(t, eRef, 160)
	advanceViaCommand(t, eRef, 200) // tick=360
	advanceViaCommand(t, eRef, 10)  // tick=370
	advanceViaCommand(t, eRef, 10)  // tick=380 (full, correct sequence)
	refDigest := compRef.StateDigest()

	store := persist.NewMemStore()
	city := persist.CityKey{TenantID: "t", CityID: "truncated-tail"}
	eLive, compLive := buildPersistedComposition(t, store, city)
	driveGameplayOnly(t, eLive)
	advanceViaCommand(t, eLive, 160)
	advanceViaCommand(t, eLive, 200)
	if _, ok, err := compLive.MaybeSnapshot(ctx, store, city); err != nil || !ok {
		t.Fatalf("MaybeSnapshot: ok=%v err=%v", ok, err)
	}
	advanceViaCommand(t, eLive, 10)
	advanceViaCommand(t, eLive, 10)

	ids, err := store.ListSnapshots(ctx, city)
	if err != nil || len(ids) != 1 {
		t.Fatalf("precondition: ListSnapshots ids=%v err=%v", ids, err)
	}
	data, err := store.GetSnapshot(ctx, city, ids[0])
	if err != nil {
		t.Fatalf("GetSnapshot: %v", err)
	}
	allCmds, err := RestoreCommands(ctx, store, city)
	if err != nil {
		t.Fatalf("RestoreCommands: %v", err)
	}

	eR := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	compR, err := Wire(eR, nil)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	snapTick, err := compR.restoreFromSnapshotBytes(data)
	if err != nil {
		t.Fatalf("restoreFromSnapshotBytes: %v", err)
	}
	fullTail, err := splitJournalAtTick(allCmds, snapTick, city, errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("splitJournalAtTick: %v", err)
	}
	if len(fullTail) < 2 {
		t.Fatalf("precondition: tail has %d commands, want at least 2 to truncate", len(fullTail))
	}
	// Deliberately DROP the final tail command (the last AdvanceTicks) --
	// this is the injected fault this test exists to catch.
	truncatedTail := fullTail[:len(fullTail)-1]
	if err := replayCommands(eR, truncatedTail); err != nil {
		t.Fatalf("replayCommands (truncated): %v", err)
	}

	if compR.StateDigest() == refDigest {
		t.Fatal("prove-can-fail: a TRUNCATED tail replay produced the SAME digest as the correct reference -- the digest-equality comparison has no teeth")
	}
	restoredClock, err := eR.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	if restoredClock.Tick() == 380 {
		t.Fatal("prove-can-fail: truncated tail still reached tick 380 -- the dropped command carried no ticks, weakening this test's fault injection")
	}
}

// TestSplitJournalAtTick_ExactBoundary proves the exact-multiple case: a
// non-advance command occurring strictly BEFORE the snapshot tick is
// excluded from the tail, and everything (advance or not) occurring at or
// after the snapshot tick is included verbatim.
func TestSplitJournalAtTick_ExactBoundary(t *testing.T) {
	adv := func(n int64) protocol.Command {
		return protocol.Command{Kind: protocol.KindAdvanceTicks, Payload: protocol.AdvanceTicksPayload{N: n}}
	}
	buy := protocol.Command{Kind: protocol.KindBuy, CorrelationID: "pre-boundary-buy"}
	zone := protocol.Command{Kind: protocol.KindZone, CorrelationID: "post-boundary-zone"}
	cmds := []protocol.Command{adv(100), buy, adv(260), zone}
	city := persist.CityKey{TenantID: "t", CityID: "split"}

	tail, err := splitJournalAtTick(cmds, 360, city, errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("splitJournalAtTick: %v", err)
	}
	if len(tail) != 1 || tail[0].CorrelationID != "post-boundary-zone" {
		t.Fatalf("tail = %v, want exactly [zone] (the pre-boundary buy must be excluded)", tail)
	}
}

// TestSplitJournalAtTick_StraddlingCommandIsSplit proves the defensive
// mid-command straddling path: an AdvanceTicks command whose N would carry
// the running tick total PAST snapshotTick (rather than landing exactly on
// it) is replaced in the tail by a synthetic AdvanceTicks command carrying
// only the remainder, so replaying the tail still advances the clock by
// exactly the same total the full journal would have.
func TestSplitJournalAtTick_StraddlingCommandIsSplit(t *testing.T) {
	adv := func(n int64) protocol.Command {
		return protocol.Command{Kind: protocol.KindAdvanceTicks, Payload: protocol.AdvanceTicksPayload{N: n}}
	}
	cmds := []protocol.Command{adv(200), adv(200)} // cumulative 200, then 400 -- snapshotTick=300 lands mid-second-command
	city := persist.CityKey{TenantID: "t", CityID: "split-straddle"}

	tail, err := splitJournalAtTick(cmds, 300, city, errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("splitJournalAtTick: %v", err)
	}
	if len(tail) != 1 {
		t.Fatalf("tail = %v, want exactly 1 synthetic AdvanceTicks command", tail)
	}
	got, ok := tail[0].Payload.(protocol.AdvanceTicksPayload)
	if !ok {
		t.Fatalf("tail[0].Payload = %T, want protocol.AdvanceTicksPayload", tail[0].Payload)
	}
	if got.N != 100 {
		t.Fatalf("tail[0].Payload.N = %d, want 100 (400-300 remainder)", got.N)
	}
}

// TestSplitJournalAtTick_ProveCanFail_JournalShorterThanSnapshotErrors
// proves a journal that never reaches the recorded snapshot tick (a
// corrupt/mismatched Store) surfaces MET-G810 rather than silently
// returning an empty or partial tail.
func TestSplitJournalAtTick_ProveCanFail_JournalShorterThanSnapshotErrors(t *testing.T) {
	adv := func(n int64) protocol.Command {
		return protocol.Command{Kind: protocol.KindAdvanceTicks, Payload: protocol.AdvanceTicksPayload{N: n}}
	}
	cmds := []protocol.Command{adv(100)} // only 100 ticks -- snapshotTick=360 is never reached
	city := persist.CityKey{TenantID: "t", CityID: "split-short"}

	_, err := splitJournalAtTick(cmds, 360, city, errs.NewCorrelationID())
	if err == nil {
		t.Fatal("prove-can-fail: an out-of-sync journal (never reaches the snapshot tick) silently succeeded")
	}
	if !errors.Is(err, &errs.E{Code: ErrSnapshotTailShort}) {
		t.Fatalf("err = %v, want to wrap ErrSnapshotTailShort", err)
	}
}

// ---------------------------------------------------------------------------
// BUG-480 — walk-back past a tail-inconsistent newest snapshot, and the
// journal-dirty gate that stops MaybeSnapshotEvery from manufacturing one
// during live operation.
// ---------------------------------------------------------------------------

// failingAppendStore fails the Nth AppendJournal call (1-based) with a
// synthetic error — the same BUG-472 swallow-modelling shape
// cmd/metroserve/attack_inc3b_test.go's attackFailingAppendStore uses, kept
// as its own small copy here so compose's own tests do not need to import
// cmd/metroserve (which would be a layering inversion — internal/ui ->
// internal/engine is banned by GR#20, and cmd -> internal is the wrong
// direction to reverse). Single-goroutine use only (every BUG-480 test
// below drives ticks synchronously via advanceViaCommand), so calls is a
// plain int64, not atomic.
type failingAppendStore struct {
	persist.Store
	calls    int64
	failCall int64
}

func (s *failingAppendStore) AppendJournal(ctx context.Context, city persist.CityKey, rec []byte) error {
	s.calls++
	if s.calls == s.failCall {
		return errors.New("test: synthetic durable-append failure")
	}
	return s.Store.AppendJournal(ctx, city, rec)
}

// TestRestoreLatestSnapshotOrGenesis_WalksBackPastTailInconsistentNewest is
// BUG-480 deliverable (a)'s headline proof: given a newest snapshot whose
// recorded tick cannot be reconciled with the durable journal (the class a
// lost journal frame produces), restore WALKS BACK to the next-older,
// still-consistent snapshot instead of bricking forever with
// ErrSnapshotTailShort.
//
// The inconsistent snapshot #2 is manufactured by calling
// buildSnapshotBytes/PutSnapshot DIRECTLY, bypassing MaybeSnapshotEvery's
// own dirty gate (deliverable (b), tested separately below) — this is
// deliberate: deliverable (b) closes the specific LIVE, same-process race
// that would otherwise let a dirty journaler write exactly this kind of bad
// snapshot, so reproducing the scenario through that same live path can no
// longer happen (that IS deliverable (b) working). Bypassing the gate here
// models every OTHER way an inconsistent snapshot can still reach a store —
// data written by a pre-BUG-480 binary, a future out-of-band/administrative
// snapshot trigger, or simply a direct probe of walk-back's own contract —
// and proves deliverable (a) is correct in complete isolation from (b).
func TestRestoreLatestSnapshotOrGenesis_WalksBackPastTailInconsistentNewest(t *testing.T) {
	ctx := context.Background()
	const cadence = int64(4)
	mem := persist.NewMemStore()
	city := persist.CityKey{TenantID: "t", CityID: "walkback-480"}
	// advanceViaCommand sends ONE AdvanceTicks{N:n} command per call, so
	// each call below is exactly one AppendJournal attempt regardless of
	// how many ticks n itself advances. failCall targets the SECOND call —
	// the very first tick advance AFTER the good snapshot at tick=cadence.
	// Under BUG-472's "HALT + SURFACE" ruling (Aaron, 2026-09-01,
	// superseding the swallow-and-continue policy this test was originally
	// written against), that second command's OWN effect still applies
	// (journalAccepted's doc comment, engine/core/commands.go) but it is
	// REJECTED and the Engine is now PERMANENTLY halted -- there is no
	// third command to drive further, so the halting command's own N is
	// sized to land exactly on the next cadence boundary (2*cadence) in
	// one step, matching what the old two-command (1 + cadence-1) sequence
	// used to produce.
	failing := &failingAppendStore{Store: mem, failCall: 2}

	e1, comp1 := buildPersistedComposition(t, failing, city)
	advanceViaCommand(t, e1, cadence) // call #1 (ok): tick=4.
	if _, ok, err := comp1.MaybeSnapshotEvery(ctx, failing, city, cadence); err != nil || !ok {
		t.Fatalf("snapshot #1 (good, tick=%d): ok=%v err=%v", cadence, ok, err)
	}

	// call #2 -- FAILS and HALTS the Engine; its own tick effect (advancing
	// by cadence) still applies, journal stuck at 4.
	advanceViaCommandExpectHalt(t, e1, cadence)
	liveTick, err := e1.Clock()
	if err != nil {
		t.Fatalf("Clock (live): %v", err)
	}
	if liveTick.Tick() != 2*cadence {
		t.Fatalf("precondition: live tick = %d, want %d", liveTick.Tick(), 2*cadence)
	}

	// Manufacture the inconsistent newest snapshot directly (see doc
	// comment above): it records comp1's CURRENT live tick (8), which the
	// durable journal (short by the halting command's own frame) can never
	// reach.
	badData, err := comp1.buildSnapshotBytes()
	if err != nil {
		t.Fatalf("buildSnapshotBytes: %v", err)
	}
	if _, err := failing.PutSnapshot(ctx, city, badData); err != nil {
		t.Fatalf("PutSnapshot (manufactured bad snapshot): %v", err)
	}

	ids, err := mem.ListSnapshots(ctx, city)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("precondition: %d snapshots, want exactly 2 (one good, one manufactured-inconsistent)", len(ids))
	}

	// The honest ground truth: replaying ONLY the commands that actually
	// persisted (the halting command's own frame is genuinely, permanently
	// lost, and nothing after it was ever attempted) from genesis. Same
	// world seed as comp1 (roundTripSeed, via buildPersistedComposition) —
	// Wire's own construction (seedCitizenCount etc.) is seed-derived and
	// genesis replay never goes through Load, so a mismatched seed here
	// would diverge the digest for a reason that has nothing to do with
	// the walk-back logic this test is actually proving.
	eGenesis := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	compGenesis, err := Wire(eGenesis, nil)
	if err != nil {
		t.Fatalf("Wire (genesis reference): %v", err)
	}
	persistedCmds, err := RestoreCommands(ctx, mem, city)
	if err != nil {
		t.Fatalf("RestoreCommands: %v", err)
	}
	if err := replayCommands(eGenesis, persistedCmds); err != nil {
		t.Fatalf("replayCommands (genesis reference): %v", err)
	}
	refClock, err := eGenesis.Clock()
	if err != nil {
		t.Fatalf("Clock (genesis reference): %v", err)
	}
	if refClock.Tick() != cadence {
		t.Fatalf("precondition: genesis-replay reference tick = %d, want %d (only call #1 ever persisted, the halting command's frame)", refClock.Tick(), cadence)
	}
	refDigest := compGenesis.StateDigest()

	// Restore: RestoreLatestSnapshotOrGenesis MUST walk back past snapshot
	// #2 (tail-inconsistent) to snapshot #1, reproducing EXACTLY the
	// genesis-replay-of-the-persisted-journal reference above — never the
	// live tick/digest, which included the lost command's effect and can
	// never be honestly reconstructed from what actually persisted.
	eR := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	compR, err := Wire(eR, nil)
	if err != nil {
		t.Fatalf("Wire (restore target): %v", err)
	}
	usedSnapshot, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eR, compR, mem, city)
	if err != nil {
		t.Fatalf("RestoreLatestSnapshotOrGenesis: walk-back should have succeeded via the older snapshot, got error: %v", err)
	}
	if !usedSnapshot {
		t.Fatal("usedSnapshot = false, want true (snapshot #1 is a valid walk-back target)")
	}
	if tick != refClock.Tick() {
		t.Fatalf("restored tick = %d, want %d (genesis-replay reference)", tick, refClock.Tick())
	}
	if compR.StateDigest() != refDigest {
		t.Fatalf("restored digest = %x, want %x (genesis-replay-of-persisted-journal reference)", compR.StateDigest(), refDigest)
	}

	// The skip must be LOUD (ErrSnapshotSkipped, GR#7/GR#1) and name the
	// skipped candidate's own tick (8, the manufactured snapshot's
	// recorded tick — NOT the tick it eventually restored to).
	foundSkip := false
	for _, entry := range errs.Recent() {
		if entry.Code != ErrSnapshotSkipped {
			continue
		}
		if cityCtx, _ := entry.Ctx["city"].(string); !strings.Contains(cityCtx, "walkback-480") {
			continue
		}
		if tickCtx, ok := entry.Ctx["tick"].(int64); ok && tickCtx == 2*cadence {
			foundSkip = true
		}
	}
	if !foundSkip {
		t.Fatal("no ErrSnapshotSkipped entry found via errs.Recent() naming this city and the skipped snapshot's tick (8) — the walk-back was not logged loudly")
	}

	// Journal-never-grows: walk-back's validation attempts run entirely on
	// a throwaway engine/composition and never touch the durable store, so
	// the journal frame count must be unchanged by the restore above.
	framesAfter, err := mem.ReadJournal(ctx, city)
	if err != nil {
		t.Fatalf("ReadJournal: %v", err)
	}
	if len(framesAfter) != len(persistedCmds) {
		t.Fatalf("journal grew during restore: %d frames, want %d (unchanged)", len(framesAfter), len(persistedCmds))
	}
}

// TestMaybeSnapshotEvery_DirtyGateRefusesAfterSwallowedAppend is BUG-480
// deliverable (b)'s direct proof: once persistCommandJournaler observes a
// failed durable AppendJournal, MaybeSnapshotEvery refuses every subsequent
// cadence-boundary snapshot for the REST OF THIS PROCESS (dirty never
// clears — see persistjournal.go's dirty field doc comment) — logging
// ErrSnapshotRefusedDirty exactly ONCE, not once per check — so no FUTURE
// snapshot can ever repeat the tail-inconsistency class the previous test
// walks back from. Since BUG-472's later "HALT + SURFACE" ruling means the
// failing command also permanently halts the Engine (no further command can
// ever run on it), "every subsequent boundary" is exercised here as
// repeated MaybeSnapshotEvery calls at the one halted tick rather than by
// advancing further ticks — see advanceViaCommandExpectHalt's doc comment.
// A restart (a fresh journaler/composition) then restores cleanly from the
// last known-good snapshot plus its consistent tail — no walk-back needed,
// because no bad snapshot was ever produced in the first place.
func TestMaybeSnapshotEvery_DirtyGateRefusesAfterSwallowedAppend(t *testing.T) {
	ctx := context.Background()
	const cadence = int64(4)
	mem := persist.NewMemStore()
	city := persist.CityKey{TenantID: "t", CityID: "dirty-gate-480"}
	failing := &failingAppendStore{Store: mem, failCall: 2}

	e1, comp1 := buildPersistedComposition(t, failing, city)
	advanceViaCommand(t, e1, cadence) // call #1 (ok): tick=4.
	if _, ok, err := comp1.MaybeSnapshotEvery(ctx, failing, city, cadence); err != nil || !ok {
		t.Fatalf("snapshot #1 (good): ok=%v err=%v", ok, err)
	}

	// call #2 -- FAILS and HALTS the Engine (BUG-472's "HALT + SURFACE"
	// ruling, Aaron 2026-09-01, superseding the swallow-and-continue policy
	// this test was originally written against): its own tick effect
	// (advancing a full cadence, landing tick=8 exactly on the NEXT
	// boundary) still applies, journal stuck at 4. There is no third
	// command to drive after this -- every further command on e1 is
	// refused outright -- so "three more cadence boundaries" below is
	// re-expressed as three more MaybeSnapshotEvery calls AT THE SAME
	// halted tick (still == 2*cadence, still a cadence boundary every
	// time ShouldSnapshotEvery evaluates it, since that check is a pure
	// function of tick/cadence with no per-call state), which equally
	// exercises "the dirty gate holds across repeated boundary checks,
	// logging exactly once" without requiring impossible further ticks.
	advanceViaCommandExpectHalt(t, e1, cadence) // tick=8 live, journal stuck at 4.

	for i := 0; i < 3; i++ {
		id, ok, err := comp1.MaybeSnapshotEvery(ctx, failing, city, cadence)
		if err != nil {
			t.Fatalf("MaybeSnapshotEvery (dirty, check %d): unexpected error %v (refusal must be ok=false,err=nil, not a fault)", i, err)
		}
		if ok {
			t.Fatalf("MaybeSnapshotEvery (dirty, check %d): ok=true, id=%q -- wrote a snapshot while dirty", i, id)
		}
	}

	ids, err := mem.ListSnapshots(ctx, city)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("store holds %d snapshots after 3 more dirty-gate checks at the halted boundary, want exactly 1 (only the pre-swallow snapshot) -- the dirty gate did not hold", len(ids))
	}

	// Exactly ONE ErrSnapshotRefusedDirty entry for this city, despite 3
	// refused checks -- the "log once" requirement.
	refusalCount := 0
	for _, entry := range errs.Recent() {
		if entry.Code != ErrSnapshotRefusedDirty {
			continue
		}
		if cityCtx, _ := entry.Ctx["city"].(string); strings.Contains(cityCtx, "dirty-gate-480") {
			refusalCount++
		}
	}
	if refusalCount != 1 {
		t.Fatalf("ErrSnapshotRefusedDirty logged %d times for this city, want exactly 1 (log-once, not per boundary) -- note errs.Recent()'s ring buffer coalesces IDENTICAL repeats into one slot with a Repeat count, so this counts DISTINCT entries, not occurrences", refusalCount)
	}

	// Restart: a FRESH journaler (dirty=false again) restores from the
	// still-only snapshot plus its consistent tail -- no walk-back needed.
	// Same world seed as e1/comp1 (roundTripSeed, via buildPersistedComposition)
	// -- BUG-479's Load-time seed check refuses a mismatched restore engine.
	eR := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	compR, err := Wire(eR, nil)
	if err != nil {
		t.Fatalf("Wire (restore target): %v", err)
	}
	usedSnapshot, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eR, compR, mem, city)
	if err != nil {
		t.Fatalf("RestoreLatestSnapshotOrGenesis: %v", err)
	}
	if !usedSnapshot {
		t.Fatal("usedSnapshot = false, want true")
	}
	// journal-reconstructable max = cadence (call #1's own frame, the ONLY
	// one that ever persisted) -- the halting command's frame (call #2,
	// which would have advanced the journal to 2*cadence) never landed.
	wantTick := cadence
	if tick != wantTick {
		t.Fatalf("restored tick = %d, want %d", tick, wantTick)
	}
}
