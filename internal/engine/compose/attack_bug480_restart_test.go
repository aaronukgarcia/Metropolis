package compose

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// BUG-480 independent destructive round (Opus r1) — the RESTART
// RE-BRICKING attack.
//
// The dirty latch that stops MaybeSnapshotEvery writing an inconsistent
// snapshot is PROCESS-LOCAL: it is a field on persistCommandJournaler and
// there is no durable marker on disk. So the obvious attack is to outlive
// it. Sequence, materialised end to end against a real on-disk
// persist.DiskStore across three simulated process lifetimes:
//
//	P1: snapshot at a clean boundary -> a swallowed AppendJournal (BUG-472
//	    policy: the command stays ACCEPTED, so the live engine tick runs
//	    permanently AHEAD of what the journal can reconstruct) -> the dirty
//	    latch refuses every later snapshot.
//	P2: the process restarts. A brand-new journaler means dirty is GONE.
//	    The city rehydrates, then keeps ticking and crosses a cadence
//	    boundary — writing a NEW newest snapshot with no latch to stop it.
//	P3: the process restarts again and must restore from that newest
//	    snapshot.
//
// The danger being probed is whether P2's snapshot ends up sitting PAST the
// P1 inconsistency in a way P3's walk-back cannot see — a newest snapshot
// that looks self-consistent yet encodes a state the journal never
// produced, which would silently resurrect the very brick BUG-480 exists to
// remove, and do so undetectably (the walk-back only skips candidates it
// can PROVE inconsistent).
//
// The test asserts the defence actually holds for the RIGHT reason, not by
// luck: after P2's restore the live tick is re-derived FROM the journal, so
// live tick and journal-reconstructable tick re-synchronise at every
// restart and the swallowed frame stops being a running skew. P3 is
// therefore required to restore from the NEWEST snapshot with NO walk-back
// at all, at a tick and a byte-exact digest matching an independent
// from-genesis replay of the durable journal.
func TestAttackBUG480_RestartAfterDirtyLatchDoesNotRebrick(t *testing.T) {
	ctx := context.Background()
	const cadence = int64(4)
	root := t.TempDir()
	city := persist.CityKey{TenantID: "t", CityID: "restart-rebrick-480"}

	newDisk := func() persist.Store {
		d, err := persist.NewDiskStore(root)
		if err != nil {
			t.Fatalf("NewDiskStore: %v", err)
		}
		return d
	}

	// ---- P1: good snapshot, then a swallowed append, then refusals -----
	disk1 := newDisk()
	failing := &nthAppendFailStore{Store: disk1, failCall: 2}
	e1 := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	comp1, err := Wire(e1, &Deps{PersistStore: failing, PersistCity: city})
	if err != nil {
		t.Fatalf("P1 Wire: %v", err)
	}
	advanceViaCommand(t, e1, cadence) // append #1 ok -> live 4, journal 4.
	if _, ok, err := comp1.MaybeSnapshotEvery(ctx, failing, city, cadence); err != nil || !ok {
		t.Fatalf("P1 snapshot at tick %d: ok=%v err=%v", cadence, ok, err)
	}
	advanceViaCommand(t, e1, 1) // append #2 FAILS (swallowed) -> live 5, journal 4.
	advanceViaCommand(t, e1, 3) // append #3 ok            -> live 8, journal 7.
	// Live tick 8 IS a cadence boundary, so pre-BUG-480 this would have
	// written a snapshot recording tick 8 that the journal (total 7) can
	// never reach. The dirty latch must refuse it.
	if _, ok, err := comp1.MaybeSnapshotEvery(ctx, failing, city, cadence); err != nil || ok {
		t.Fatalf("P1 boundary after swallow: ok=%v err=%v, want ok=false err=nil (dirty gate must refuse)", ok, err)
	}
	if ids, err := disk1.ListSnapshots(ctx, city); err != nil || len(ids) != 1 {
		t.Fatalf("P1 end: %d snapshots (err=%v), want exactly 1", len(ids), err)
	}

	// ---- P2: restart. The latch is gone; the city must re-sync ---------
	disk2 := newDisk()
	guard2 := &replayGuardStore{Store: disk2}
	guard2.replaying.Store(true)
	e2 := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	comp2, err := Wire(e2, &Deps{PersistStore: guard2, PersistCity: city})
	if err != nil {
		t.Fatalf("P2 Wire: %v", err)
	}
	usedSnapshot, tick2, err := RestoreLatestSnapshotOrGenesis(ctx, e2, comp2, disk2, city)
	if err != nil {
		t.Fatalf("P2 rehydrate: %v", err)
	}
	guard2.replaying.Store(false)
	if !usedSnapshot {
		t.Fatal("P2: usedSnapshot = false, want true (the P1 snapshot is clean and must be used)")
	}
	// The restored tick is derived from the JOURNAL (4+3), NOT from P1's
	// live tick (8) — the swallowed command's tick is honestly lost. This
	// is the property that stops the skew from compounding across restarts.
	if wantTick := cadence + 3; tick2 != wantTick {
		t.Fatalf("P2 restored tick = %d, want %d (journal-reconstructable, one tick behind P1 live) -- if this equals P1 live tick the skew was silently carried forward", tick2, wantTick)
	}

	// Now tick past the next cadence boundary with a HEALTHY store: a new
	// snapshot is written, unguarded by any latch.
	advanceViaCommand(t, e2, 1) // live 8, journal total 8.
	id2, ok, err := comp2.MaybeSnapshotEvery(ctx, guard2, city, cadence)
	if err != nil || !ok {
		t.Fatalf("P2 snapshot after restart: ok=%v err=%v, want a snapshot to be written (a fresh journaler is not dirty)", ok, err)
	}
	ids2, err := disk2.ListSnapshots(ctx, city)
	if err != nil {
		t.Fatalf("P2 ListSnapshots: %v", err)
	}
	if len(ids2) != 2 || ids2[len(ids2)-1] != id2 {
		t.Fatalf("P2: snapshots = %v, want 2 with %q newest", ids2, id2)
	}

	// ---- Independent ground truth: genesis replay of what persisted ----
	disk3 := newDisk()
	eRef := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	compRef, err := Wire(eRef, nil)
	if err != nil {
		t.Fatalf("reference Wire: %v", err)
	}
	cmds, err := RestoreCommands(ctx, disk3, city)
	if err != nil {
		t.Fatalf("RestoreCommands: %v", err)
	}
	if err := replayCommands(eRef, cmds); err != nil {
		t.Fatalf("reference replay: %v", err)
	}
	refClock, err := eRef.Clock()
	if err != nil {
		t.Fatalf("reference Clock: %v", err)
	}

	// ---- P3: restart again; the NEWEST snapshot must be usable ---------
	// Count skips so the assertion is that P3 needed NO walk-back at all —
	// a newest snapshot that had to be skipped here would mean the P1
	// inconsistency did survive the restart into P2's snapshot.
	skipsBefore := countSkips(city.CityID)
	guard3 := &replayGuardStore{Store: disk3}
	guard3.replaying.Store(true)
	e3 := core.NewEngine(core.WithWorldSeed(roundTripSeed), core.WithPoolSize(1))
	comp3, err := Wire(e3, &Deps{PersistStore: guard3, PersistCity: city})
	if err != nil {
		t.Fatalf("P3 Wire: %v", err)
	}
	usedSnapshot3, tick3, err := RestoreLatestSnapshotOrGenesis(ctx, e3, comp3, disk3, city)
	if err != nil {
		t.Fatalf("P3 rehydrate BRICKED: %v -- the post-restart snapshot is unusable, which is the BUG-480 failure mode reintroduced across a process boundary", err)
	}
	guard3.replaying.Store(false)
	if !usedSnapshot3 {
		t.Fatal("P3: usedSnapshot = false -- fell all the way back to genesis, so the post-restart snapshot was not usable")
	}
	if got := countSkips(city.CityID) - skipsBefore; got != 0 {
		t.Fatalf("P3 walked back past %d snapshot(s); want 0 -- the newest (post-restart) snapshot should be directly usable, and needing a skip means P2 wrote an inconsistent one", got)
	}
	if tick3 != refClock.Tick() {
		t.Fatalf("P3 restored tick = %d, want %d (genesis reference)", tick3, refClock.Tick())
	}
	if comp3.StateDigest() != compRef.StateDigest() {
		t.Fatalf("P3 digest = %x, want %x (genesis replay of the durable journal)", comp3.StateDigest(), compRef.StateDigest())
	}

	// The journal must never have grown during either rehydrate (the guard
	// suppresses re-append; walk-back validation runs on throwaways only).
	framesAfter, err := disk3.ReadJournal(ctx, city)
	if err != nil {
		t.Fatalf("ReadJournal: %v", err)
	}
	if len(framesAfter) != len(cmds) {
		t.Fatalf("journal grew across restarts: %d frames, want %d", len(framesAfter), len(cmds))
	}
}

// countSkips returns how many ErrSnapshotSkipped entries in the recent-error
// ring name cityID.
func countSkips(cityID string) int {
	n := 0
	for _, entry := range errs.Recent() {
		if entry.Code != ErrSnapshotSkipped {
			continue
		}
		if c, _ := entry.Ctx["city"].(string); strings.Contains(c, cityID) {
			n++
		}
	}
	return n
}

// nthAppendFailStore fails the Nth AppendJournal call (1-based). Its counter
// is atomic so it is safe to reuse in a -race run, unlike the builder
// single-goroutine-only failingAppendStore.
type nthAppendFailStore struct {
	persist.Store
	calls    atomic.Int64
	failCall int64
}

func (s *nthAppendFailStore) AppendJournal(ctx context.Context, city persist.CityKey, rec []byte) error {
	if s.calls.Add(1) == s.failCall {
		return errors.New("attack: synthetic durable-append failure")
	}
	return s.Store.AppendJournal(ctx, city, rec)
}

// replayGuardStore is a local stand-in for cmd/metroserve rehydrateGuardStore
// (unexported there, and cmd -> internal is the wrong import direction): it
// suppresses AppendJournal while a rehydrate replay is in flight so restore
// never double-appends, while every READ passes straight through.
type replayGuardStore struct {
	persist.Store
	replaying atomic.Bool
}

func (g *replayGuardStore) AppendJournal(ctx context.Context, city persist.CityKey, rec []byte) error {
	if g.replaying.Load() {
		return nil
	}
	return g.Store.AppendJournal(ctx, city, rec)
}
