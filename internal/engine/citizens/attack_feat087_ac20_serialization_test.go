package citizens

import (
	"reflect"
	"sync"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/det"
)

// attack_feat087_ac20_serialization_test.go — FEAT-087 AC-20 (BUG-483 F3):
// DeathQueue-pending serialization. A citizen selected-but-not-yet-realised
// must survive a save+restore cycle byte-identically, and the derived
// shardIndex membership mirror (BUG-663) MUST be rebuilt on restore — a
// missed rebuild leaves every restored pending citizen IMMORTAL (queued
// forever, realised never; see DeathQueueSnapshot's doc in deathwave.go).
//
// Two altitudes:
//   - DeathQueue-level (Snapshot/RestoreSnapshot directly): deterministic,
//     no population/mortality noise, exercises the exact mechanics AC-20
//     is about.
//   - CitizensAPI-level (through the real save.Participant wire,
//     citizensSaveInto/reloadFrom from participant_test.go): proves the
//     mechanism is actually WIRED into the real save path, not just
//     reachable in isolation.

// --- DeathQueue-level: the AC's own literal test description. ---

// TestFEAT087AC20_PendingSurvivesSnapshotRoundTrip mirrors the acceptance
// doc's own "Test" paragraph verbatim: enqueue N citizens, realise NONE,
// snapshot ("save"), restore into a fresh queue ("reload"), THEN drain —
// RealisedSequence from the restored queue must equal what draining the
// never-saved original would have produced.
func TestFEAT087AC20_PendingSurvivesSnapshotRoundTrip(t *testing.T) {
	const cid = "ac20"
	const month = int64(500)

	control := NewDeathQueue()
	saved := NewDeathQueue()
	for _, id := range []uint64{500, 10, 999, 1, 42} {
		must(t, control.Enqueue(id, month, cid))
		must(t, saved.Enqueue(id, month, cid))
	}

	// "Save": snapshot `saved`, restore into a fresh queue standing in for
	// the reloaded engine.
	snap := saved.Snapshot(cid)
	reloaded := NewDeathQueue()
	must(t, reloaded.RestoreSnapshot(snap, cid))

	// Drain BOTH the never-saved control and the restored queue with an
	// identical budget/month and compare.
	const budget = 10
	gotControl := control.Realise(budget, month+1, cid)
	gotReloaded := reloaded.Realise(budget, month+1, cid)
	if !reflect.DeepEqual(gotControl, gotReloaded) {
		t.Fatalf("restored queue drained a different set/order than the never-saved control:\n control=%v\n reloaded=%v", gotControl, gotReloaded)
	}
	if !reflect.DeepEqual(control.RealisedSequence(cid), reloaded.RealisedSequence(cid)) {
		t.Fatalf("RealisedSequence differs after drain: control=%v reloaded=%v",
			control.RealisedSequence(cid), reloaded.RealisedSequence(cid))
	}
	if control.Len(cid) != reloaded.Len(cid) {
		t.Fatalf("pending length differs after drain: control=%d reloaded=%d", control.Len(cid), reloaded.Len(cid))
	}
}

// TestFEAT087AC20_ConservationAcrossSnapshotBoundary proves AC-2's
// totalRealised+pending==totalSelected conservation invariant holds ACROSS
// the save/restore boundary — the exact property the pre-AC-20 "KNOWN GAP"
// disclosure in participant_test.go said was NOT guaranteed.
func TestFEAT087AC20_ConservationAcrossSnapshotBoundary(t *testing.T) {
	const cid = "ac20-cons"
	q := NewDeathQueue()

	totalSelected := 0
	for m := int64(0); m < 5; m++ {
		for i := uint64(0); i < 20; i++ {
			id := uint64(m)*100 + i + 1
			must(t, q.Enqueue(id, m, cid))
			totalSelected++
		}
		// Realise a partial budget each month, same shape RealiseDrained's
		// own callers use (some released, some deferred).
		q.Realise(7, m, cid)
	}
	inFlightBeforeSave := q.Len(cid)
	realisedBeforeSave := q.TotalRealised(cid)
	if inFlightBeforeSave+realisedBeforeSave != totalSelected {
		t.Fatalf("test setup: conservation does not even hold pre-save: pending=%d realised=%d selected=%d",
			inFlightBeforeSave, realisedBeforeSave, totalSelected)
	}

	// Save + restore.
	snap := q.Snapshot(cid)
	reloaded := NewDeathQueue()
	must(t, reloaded.RestoreSnapshot(snap, cid))

	if reloaded.Len(cid) != inFlightBeforeSave {
		t.Fatalf("pending count changed across restore: before=%d after=%d", inFlightBeforeSave, reloaded.Len(cid))
	}
	if reloaded.TotalRealised(cid) != realisedBeforeSave {
		t.Fatalf("realised count changed across restore: before=%d after=%d", realisedBeforeSave, reloaded.TotalRealised(cid))
	}

	// Continue realising the remainder post-restore; conservation must still
	// hold against the ORIGINAL totalSelected, i.e. across the boundary.
	for i := 0; i < 50 && reloaded.Len(cid) > 0; i++ {
		reloaded.Realise(7, int64(100+i), cid)
	}
	if reloaded.Len(cid) != 0 {
		t.Fatalf("test setup: queue never fully drained (budget/iterations too small)")
	}
	if reloaded.TotalRealised(cid) != totalSelected {
		t.Fatalf("AC-2 conservation broken across save/restore: totalRealised=%d want=%d",
			reloaded.TotalRealised(cid), totalSelected)
	}
}

// --- The mandatory shardIndex rebuild + immortal-citizen regression. ---

// TestFEAT087AC20_ShardIndexRebuiltOnRestore is the mandatory BUG-663 r3
// proof: a citizen restored from a snapshot must be
// IsQueuedInShard-visible in ITS OWN shard immediately after restore (the
// live cold pass, registry.go's applyMonthly, queries membership through
// IsQueuedInShard ONLY — never through the whole-queue `queued` map — so a
// stale/empty shardIndex after restore is a silent immortal citizen: never
// re-selected because it is already `queued`, and never realised because
// the day-tick path never sees it as pending). Citizens are chosen to land
// in DISTINCT shards so a rebuild that only fixes shard 0 (or any other
// single-shard-shaped bug) would still be caught.
func TestFEAT087AC20_ShardIndexRebuiltOnRestore(t *testing.T) {
	const cid = "ac20-shard"
	q := NewDeathQueue()

	ids := make([]uint64, 0, 6)
	seenShards := map[int]bool{}
	for candidate := uint64(1); len(ids) < 6; candidate++ {
		shard := det.ShardForEntity(candidate)
		if seenShards[shard] {
			continue
		}
		seenShards[shard] = true
		ids = append(ids, candidate)
	}
	for _, id := range ids {
		must(t, q.Enqueue(id, 10, cid))
	}

	snap := q.Snapshot(cid)
	reloaded := NewDeathQueue()
	must(t, reloaded.RestoreSnapshot(snap, cid))

	for _, id := range ids {
		shard := det.ShardForEntity(id)
		if !reloaded.IsQueuedInShard(shard, id, cid) {
			t.Fatalf("citizen %d (shard %d): IsQueuedInShard is FALSE immediately after restore — "+
				"shardIndex was not rebuilt, this citizen is now IMMORTAL (BUG-663 r3 hazard)", id, shard)
		}
	}

	// Prove they are not just index-visible but actually REALISABLE — an
	// immortal citizen would pass the IsQueuedInShard check above (if the
	// bug were instead "index says queued but drain skips it") yet never
	// leave the queue. Drain generously and confirm every one of them is
	// gone from BOTH the queue and the shard index.
	released := reloaded.Realise(len(ids), 11, cid)
	if len(released) != len(ids) {
		t.Fatalf("restored citizens did not all realise: released=%d want=%d", len(released), len(ids))
	}
	for _, id := range ids {
		shard := det.ShardForEntity(id)
		if reloaded.IsQueuedInShard(shard, id, cid) {
			t.Fatalf("citizen %d: still IsQueuedInShard=true after being realised — shardIndex removal broken", id)
		}
		if _, stillQueued := reloaded.IsQueued(id, cid); stillQueued {
			t.Fatalf("citizen %d: still IsQueued=true after being realised", id)
		}
	}
}

// TestFEAT087AC20_RestoreSnapshotRaceAgainstIsQueuedInShard is the round's
// own regression (BLOCKING REJECT): RestoreSnapshot's shardIndex-rebuild
// loop originally wrote `q.shardIndex[i] = nil` under q.mu ALONE, but
// IsQueuedInShard reads that same slot under shardMu[i] alone (this file's
// own leaf-lock design — IsQueuedInShard never touches q.mu at all), so the
// nil-out and a concurrent shard read were UNSYNCHRONIZED: a plain data
// race on q.shardIndex[i], caught by -race, independent of whether either
// side ever observed a wrong boolean. Reproduces the attacker's exact
// hammer shape: a swarm of readers hammering IsQueuedInShard against a
// swarm of RestoreSnapshot calls on a fixed, non-trivial (200-entry)
// pending set, run under `-race`. This test is a NO-OP under a normal `go
// test` run (it never asserts on the booleans IsQueuedInShard returns,
// since those are inherently racy-in-the-ordinary-sense across concurrent
// restores) — its entire job is to give the race detector enough
// concurrent shard-index traffic to catch an unguarded write the moment one
// is reintroduced.
func TestFEAT087AC20_RestoreSnapshotRaceAgainstIsQueuedInShard(t *testing.T) {
	const cid = "ac20-race"
	const numPending = 200
	const numReaders = 8
	const numRestores = 50

	// Build a snapshot with 200 pending entries spread across many shards —
	// matching the attacker's reproduction shape (8 goroutines x 50
	// restores x 200 pending).
	seed := NewDeathQueue()
	for i := uint64(1); i <= numPending; i++ {
		must(t, seed.Enqueue(i, 1, cid))
	}
	snap := seed.Snapshot(cid)

	q := NewDeathQueue()
	must(t, q.RestoreSnapshot(snap, cid))

	stop := make(chan struct{})
	var readersDone sync.WaitGroup
	readersDone.Add(numReaders)
	for r := 0; r < numReaders; r++ {
		go func(seedID uint64) {
			defer readersDone.Done()
			id := uint64(1)
			for {
				select {
				case <-stop:
					return
				default:
				}
				shard := det.ShardForEntity(id)
				q.IsQueuedInShard(shard, id, cid)
				id = id%numPending + 1
			}
		}(uint64(r))
	}

	for i := 0; i < numRestores; i++ {
		must(t, q.RestoreSnapshot(snap, cid))
	}
	close(stop)
	readersDone.Wait()
}

// TestFEAT087AC20_OldSaveDecodesToEmptyQueue proves GR#16-style backward
// compatibility: a bundle that predates AC-20 (no "citizens.deathqueue"
// record at all — every save this package ever wrote before this commit)
// decodes cleanly into the same empty queue NewDeathQueue constructs,
// never a decode error, and the rest of the population still loads.
func TestFEAT087AC20_OldSaveDecodesToEmptyQueue(t *testing.T) {
	const seed = uint64(555)
	src := buildPopulation(t, seed, "old-src") // has real pending/realised state
	pull := NewSaveParticipant(src).Source()

	target, err := NewCitizensAPI(seed, "old-target")
	must(t, err)
	handle := NewSaveParticipant(target).Handler()

	sawDeathQueueRecord := false
	for {
		rec, ok, err := pull()
		must(t, err)
		if !ok {
			break
		}
		if rec.Kind == recCitizensDeathQueue {
			// Simulate a save bundle written by pre-AC-20 code: this record
			// never existed, so an "old save" replay never emits it.
			sawDeathQueueRecord = true
			continue
		}
		must(t, handle(rec))
	}
	if !sawDeathQueueRecord {
		t.Fatalf("test setup: source never emitted a %s record to skip", recCitizensDeathQueue)
	}

	got := target.deathQueue.Snapshot("old-target")
	want := DeathQueueSnapshot{
		Pending:     []DeathQueueEntrySnapshot{},
		RealisedIDs: []uint64{},
		RealisedAt:  map[uint64]int64{},
		Handoff:     []RealisedDeath{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("old-save decode: want the empty NewDeathQueue default, got %+v", got)
	}
	if len(coldRecordsOf(target)) == 0 {
		t.Fatalf("old-save decode: cold population failed to load alongside the skipped death-queue record")
	}
}

// TestFEAT087AC20_ContinueMatchesNeverSavedControl is the CitizensAPI-level
// differential oracle (the FEAT-087 estate's own established pattern,
// attack_feat087_inc3_handoff_test.go): a save+restore boundary in the
// middle of a run must be invisible to everything that happens afterwards.
// Two identical populations run in lockstep; one is saved and reloaded
// mid-run, the other never is; both then continue for 24 more months of
// real AdvanceDayTick (natural mortality hazard draws included) and must
// end up in an IDENTICAL state — cold records, PopulationHash, AND the
// death queue's pending set / realised sequence / handoff stream
// (assertSameState, extended for AC-20 above).
func TestFEAT087AC20_ContinueMatchesNeverSavedControl(t *testing.T) {
	const seed = uint64(2024)
	control := buildPopulation(t, seed, "control")
	saveSrc := buildPopulation(t, seed, "presave")

	root := citizensSaveInto(t, saveSrc, "presave")
	reloaded := reloadFrom(t, root, seed, "reloaded")

	// Sanity: identical immediately after restore (participant_test.go's
	// own round-trip proof, re-asserted here as this test's baseline).
	assertSameState(t, control, reloaded, "post-restore-baseline")

	// Continue BOTH for 24 months of real day-ticks — long enough for the
	// natural Gompertz-Makeham hazard to select and realise fresh deaths on
	// both sides, genuinely exercising Enqueue/Realise/RealiseDrained
	// post-restore, not just replaying a frozen snapshot.
	const months = 24
	for m := 0; m < months; m++ {
		for d := 0; d < DaysPerMonth; d++ {
			_, _, err := control.AdvanceDayTick("control")
			must(t, err)
			_, _, err = reloaded.AdvanceDayTick("reloaded")
			must(t, err)
		}
	}
	assertSameState(t, control, reloaded, "post-24-months")
}
