package compose

import (
	"context"
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/save"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// BUG-488 — the BUG-479 sibling gap: BUG-479 made a restored SAVE BUNDLE
// (a snapshot) refuse a foreign world seed via its header (MET-E819), but
// the JOURNAL-ONLY genesis-replay branch of RestoreLatestSnapshotOrGenesis
// (reached whenever a city has zero durable snapshots — every city's first
// SnapshotCadenceTicks, or permanently under --snapshot-every 0) had NO
// seed check at all: a bare genesis journal carries no bundle header to
// validate against. The BUG-479 r2 round measured this empirically:
// replaying city A's 5 journal records into a differently-seeded engine
// returned err=nil.
//
// The fix (snapshot.go's checkOrStampWorldSeed, called at the top of
// RestoreLatestSnapshotOrGenesis) stamps a city's ORIGINATING world seed
// durably at the persist layer (persist.Store.SetWorldSeedIfAbsent,
// called from compose.Wire the moment persistence is wired for a city)
// and validates a restoring engine's seed against it before touching a
// single journal record.

const (
	bug488SeedA = uint64(488001)
	bug488SeedB = uint64(488002)
)

// bug488JournalOnlyCommands is a short, deterministic, fully-accepted
// command sequence — deliberately far short of SnapshotCadenceTicks (360
// ticks) so no snapshot is ever taken and RestoreLatestSnapshotOrGenesis is
// forced down the genesis-replay branch this bug is about, exactly the
// "5 accepted commands, zero snapshots" shape the BUG-479 round measured.
func bug488JournalOnlyCommands() []protocol.Command {
	cell := protocol.CellRef{X: 2, Y: 2}
	mk := func(id string, kind protocol.Kind, payload protocol.CommandPayload) protocol.Command {
		return protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(id),
			Kind:            kind,
			Payload:         payload,
		}
	}
	return []protocol.Command{
		mk("bug488-buy", protocol.KindBuy, protocol.BuyPayload{Cell: cell}),
		mk("bug488-zone", protocol.KindZone, protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"}),
		mk("bug488-build", protocol.KindBuild, protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"}),
		mk("bug488-adv-1", protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 5}),
		mk("bug488-adv-2", protocol.KindAdvanceTicks, protocol.AdvanceTicksPayload{N: 3}),
	}
}

// TestAttackBUG488_JournalOnlyCrossCityRestore_Refused is the RED proof:
// this is the exact scenario the BUG-479 round measured returning err=nil.
// City A is driven through bug488JournalOnlyCommands with persistence
// wired at seed A and NO snapshot ever taken; a fresh composition wired
// at a DIFFERENT seed B then attempts RestoreLatestSnapshotOrGenesis
// against city A's store records. It must now be refused with
// save.ErrSaveSeedMismatch (MET-E819), before any command is replayed, and
// must leave the seed-B composition at its pristine digest and tick 0.
func TestAttackBUG488_JournalOnlyCrossCityRestore_Refused(t *testing.T) {
	ctx := context.Background()
	store := persist.NewMemStore()
	cityA := persist.CityKey{TenantID: "tenant-488", CityID: "city-a"}

	eA := core.NewEngine(core.WithWorldSeed(bug488SeedA), core.WithPoolSize(1))
	if _, err := Wire(eA, &Deps{PersistStore: store, PersistCity: cityA}); err != nil {
		t.Fatalf("Wire (city A): %v", err)
	}
	submitAll(t, eA, bug488JournalOnlyCommands())

	// Precondition: no snapshot exists for city A — this restore MUST go
	// through the genesis-replay branch, not the (already-protected)
	// snapshot walk-back.
	if ids, err := store.ListSnapshots(ctx, cityA); err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	} else if len(ids) != 0 {
		t.Fatalf("precondition: city A has %d snapshots, want 0 (journal-only case)", len(ids))
	}
	if journal, err := store.ReadJournal(ctx, cityA); err != nil {
		t.Fatalf("ReadJournal: %v", err)
	} else if len(journal) != len(bug488JournalOnlyCommands()) {
		t.Fatalf("precondition: city A journal has %d records, want %d", len(journal), len(bug488JournalOnlyCommands()))
	}

	// City B: a composition on a DIFFERENT world seed, pointed at city A's
	// records (the mis-keyed-restore / copied-store scenario).
	eB := core.NewEngine(core.WithWorldSeed(bug488SeedB), core.WithPoolSize(1))
	compB, err := Wire(eB, nil)
	if err != nil {
		t.Fatalf("Wire (city B): %v", err)
	}
	_, pristineB := buildCompositionWithSeed(t, bug488SeedB)
	pristineDigest := pristineB.StateDigest()
	if compB.StateDigest() != pristineDigest {
		t.Fatal("fixture bug: two freshly Wired city-B compositions disagree before any restore")
	}

	usedSnapshot, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eB, compB, store, cityA)
	if err == nil {
		t.Fatalf("city A's journal-only records replayed into city B's composition with NO error (usedSnapshot=%v tick=%d) — this is exactly the BUG-479 round's measured cross-city divergence, now supposedly closed by BUG-488", usedSnapshot, tick)
	}
	if !errors.Is(err, &errs.E{Code: save.ErrSaveSeedMismatch}) {
		t.Fatalf("restore error = %v, want %s", err, save.ErrSaveSeedMismatch)
	}
	// BUG-317/BUG-357 class check: the registered MET-E819 template
	// interpolates {bundleSeed}/{compositionSeed} -- prove this call site
	// actually supplies ctx keys the template resolves, not a literal
	// unresolved `{token}` in the rendered message.
	assertRendered(t, save.ErrSaveSeedMismatch, cityA.CityID, []string{
		"does not match loading composition seed",
	})

	if got := compB.StateDigest(); got != pristineDigest {
		t.Fatalf("city B's composition state CHANGED on a refused cross-city journal-only restore: pristine=%x post-refusal=%x", pristineDigest, got)
	}
	clockB, err := eB.Clock()
	if err != nil {
		t.Fatalf("eB.Clock: %v", err)
	}
	if clockB.Tick() != 0 {
		t.Fatalf("refused cross-city journal-only restore still moved city B's clock to tick %d — the replay leaked through the refusal", clockB.Tick())
	}
}

// TestAttackBUG488_JournalOnlySameCityRestore_StillWorks is the
// prove-can-fail companion: an identical journal-only restore into a
// composition on the SAME seed must still succeed via genesis replay,
// reproducing city A's exact digest and tick — so the refusal above is
// attributable to the seed check alone, not to anything else about the
// journal-only fixture.
func TestAttackBUG488_JournalOnlySameCityRestore_StillWorks(t *testing.T) {
	ctx := context.Background()
	store := persist.NewMemStore()
	cityA := persist.CityKey{TenantID: "tenant-488", CityID: "city-a"}

	eA := core.NewEngine(core.WithWorldSeed(bug488SeedA), core.WithPoolSize(1))
	compA, err := Wire(eA, &Deps{PersistStore: store, PersistCity: cityA})
	if err != nil {
		t.Fatalf("Wire (city A): %v", err)
	}
	submitAll(t, eA, bug488JournalOnlyCommands())
	wantDigest := compA.StateDigest()
	wantTick, err := engineTick(eA, "bug488-same")
	if err != nil {
		t.Fatalf("engineTick: %v", err)
	}

	eR := core.NewEngine(core.WithWorldSeed(bug488SeedA), core.WithPoolSize(1))
	compR, err := Wire(eR, nil)
	if err != nil {
		t.Fatalf("Wire (restore target): %v", err)
	}
	usedSnapshot, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eR, compR, store, cityA)
	if err != nil {
		t.Fatalf("same-seed journal-only restore failed: %v — the cross-city refusal cannot be attributed to the seed check unless this succeeds", err)
	}
	if usedSnapshot {
		t.Fatal("same-seed journal-only restore reports usedSnapshot=true, want false (no snapshot was ever taken)")
	}
	if tick != wantTick {
		t.Fatalf("same-seed journal-only restore tick = %d, want %d", tick, wantTick)
	}
	if compR.StateDigest() != wantDigest {
		t.Fatalf("same-seed journal-only restore digest mismatch: got %x, want %x", compR.StateDigest(), wantDigest)
	}
}

// TestAttackBUG488_PreFixJournalWithNoStoredSeed_LoadsUnderExplicitDefault
// proves the explicit backward-compatible default for (c): a journal that
// predates BUG-488 (its records were appended directly to the store,
// bypassing compose.Wire's seed-stamping entirely — the shape every
// journal durably written before this fix actually has) carries NO
// recorded seed at all. Restoring it must NOT be refused (that would be a
// silent economy-breaking regression for every already-persisted city the
// moment this fix ships) — it loads exactly as it did before BUG-488,
// and the restoring engine's seed is stamped as the city's seed of record
// from this point forward (proven by the second restore call below, which
// DOES now get refused on a genuine mismatch).
func TestAttackBUG488_PreFixJournalWithNoStoredSeed_LoadsUnderExplicitDefault(t *testing.T) {
	ctx := context.Background()
	store := persist.NewMemStore()
	city := persist.CityKey{TenantID: "tenant-488", CityID: "legacy-city"}

	// Simulate a PRE-BUG-488 durable journal: append encoded command
	// frames straight to the store, exactly as persistCommandJournaler
	// does, but WITHOUT ever going through compose.Wire (so
	// SetWorldSeedIfAbsent is never called — no seed.json sidecar, the
	// real shape of every journal durably written before this fix).
	for _, cmd := range bug488JournalOnlyCommands() {
		data, err := protocol.EncodeCommand(cmd)
		if err != nil {
			t.Fatalf("EncodeCommand: %v", err)
		}
		if err := store.AppendJournal(ctx, city, data); err != nil {
			t.Fatalf("AppendJournal: %v", err)
		}
	}
	if _, ok, err := store.WorldSeed(ctx, city); err != nil {
		t.Fatalf("WorldSeed precondition: %v", err)
	} else if ok {
		t.Fatal("precondition: legacy fixture already has a recorded seed")
	}

	// First restore, at seed A: no seed is on record, so this must load
	// via ordinary genesis replay -- no refusal, no error -- the explicit
	// backward-compatible default.
	eFirst := core.NewEngine(core.WithWorldSeed(bug488SeedA), core.WithPoolSize(1))
	compFirst, err := Wire(eFirst, nil)
	if err != nil {
		t.Fatalf("Wire (first restore): %v", err)
	}
	usedSnapshot, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eFirst, compFirst, store, city)
	if err != nil {
		t.Fatalf("legacy no-seed journal restore refused: %v -- old journals with no stored seed must load under the explicit backward-compatible default (no check possible), never be refused", err)
	}
	if usedSnapshot {
		t.Fatal("legacy fixture unexpectedly reports usedSnapshot=true")
	}
	if tick == 0 {
		t.Fatal("legacy no-seed journal restore left the engine at tick 0 -- the journal was not actually replayed")
	}

	// The backward-compat restore must have BACKFILLED the seed of
	// record, so a SECOND restore attempt at a genuinely different seed
	// is now caught going forward.
	recorded, ok, err := store.WorldSeed(ctx, city)
	if err != nil {
		t.Fatalf("WorldSeed after first restore: %v", err)
	}
	if !ok {
		t.Fatal("first restore did not backfill a seed of record for the legacy city -- future restores of this city stay permanently unprotected")
	}
	if recorded != bug488SeedA {
		t.Fatalf("backfilled seed = %d, want %d (the first restoring engine's own seed)", recorded, bug488SeedA)
	}

	eSecond := core.NewEngine(core.WithWorldSeed(bug488SeedB), core.WithPoolSize(1))
	compSecond, err := Wire(eSecond, nil)
	if err != nil {
		t.Fatalf("Wire (second restore): %v", err)
	}
	if _, _, err := RestoreLatestSnapshotOrGenesis(ctx, eSecond, compSecond, store, city); err == nil {
		t.Fatal("second restore at a different seed succeeded with no error -- the backward-compat backfill from the first restore should now protect this city")
	} else if !errors.Is(err, &errs.E{Code: save.ErrSaveSeedMismatch}) {
		t.Fatalf("second restore error = %v, want %s", err, save.ErrSaveSeedMismatch)
	}
}

// TestAttackBUG488_SnapshotPathUnaffected proves deliverable (d): the
// BUG-479 snapshot-restore path (RestoreLatestSnapshotOrGenesis's walk-back
// branch, entered once a snapshot exists) still restores correctly and
// still enforces its own pre-existing seed check -- BUG-488's new
// persist-layer check (which now also runs first, since it sits at the
// top of RestoreLatestSnapshotOrGenesis unconditionally) does not double
// up, misfire, or interfere with the snapshot branch's own correctness.
func TestAttackBUG488_SnapshotPathUnaffected(t *testing.T) {
	ctx := context.Background()
	store := persist.NewMemStore()
	city := persist.CityKey{TenantID: "tenant-488", CityID: "snap-city"}

	eLive, compLive := buildPersistedComposition(t, store, city)
	driveGameplayOnly(t, eLive)
	advanceViaCommand(t, eLive, 160)
	advanceViaCommand(t, eLive, 200) // tick 360 — the cadence boundary
	if _, ok, err := compLive.MaybeSnapshot(ctx, store, city); err != nil {
		t.Fatalf("MaybeSnapshot: %v", err)
	} else if !ok {
		t.Fatal("precondition: expected a snapshot at tick 360, got none")
	}
	advanceViaCommand(t, eLive, 10)

	// Same-seed restore must still use the snapshot and reproduce the
	// exact digest/tick, exactly as the pre-BUG-488 behaviour.
	eR := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	compR, err := Wire(eR, nil)
	if err != nil {
		t.Fatalf("Wire (restore target): %v", err)
	}
	usedSnapshot, tick, err := RestoreLatestSnapshotOrGenesis(ctx, eR, compR, store, city)
	if err != nil {
		t.Fatalf("same-seed snapshot restore failed: %v", err)
	}
	if !usedSnapshot {
		t.Fatal("same-seed restore did not use the snapshot")
	}
	if tick != 370 {
		t.Fatalf("same-seed restore tick = %d, want 370", tick)
	}
	if compR.StateDigest() != compLive.StateDigest() {
		t.Fatalf("same-seed restore digest mismatch: got %x, want %x", compR.StateDigest(), compLive.StateDigest())
	}
}
