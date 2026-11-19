package compose

// FEAT-9995385620 round r4: the pre-check is the restore run dry. These tests
// pin what a dry run may and may not do.

import (
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/rules"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// normalCityStore builds a store holding a version-0 city: a session, a
// snapshot, a second session (so the snapshot has a journal tail).
func normalCityStore(t *testing.T) persist.Store {
	t.Helper()
	store := persist.NewMemStore()
	rc := newRuleCity(t, store, 0)
	rc.sessionA(t)
	rc.snapshot(t, store, 5)
	rc.sessionB(t)
	return store
}

func dryRun(t *testing.T, ro *DryRunStore, seed uint64) error {
	t.Helper()
	return precheckRuleVersionsDeps(context.Background(), ro, ruleTestCity, seed, &Deps{Coastal: quietCoastal(t)})
}

// A normal city: the dry run attempts NO write at all (the real restore stamps
// nothing when the seed is on record, and it is: Wire stamped it), reads the
// journal once, lists the snapshots once and reads one snapshot.
func TestDryRun_NormalCity_NoWriteAttempted_ReadCounts(t *testing.T) {
	store := normalCityStore(t)
	before := fingerprint(t, store)
	ro := NewDryRunStore(store)
	if err := dryRun(t, ro, ruleTestSeed); err != nil {
		t.Fatalf("clean city refused: %v", err)
	}
	c := ro.Counts()
	t.Logf("dry run reads: journal=%d listSnapshots=%d getSnapshot=%d; write attempts=%v", c.JournalReads, c.SnapshotLists, c.SnapshotReads, c.Writes)
	if len(c.Writes) != 0 {
		t.Fatalf("the dry run attempted writes: %v", c.Writes)
	}
	if c.JournalReads != 1 || c.SnapshotLists != 1 || c.SnapshotReads != 1 {
		t.Fatalf("reads journal=%d list=%d snapshot=%d, want 1/1/1", c.JournalReads, c.SnapshotLists, c.SnapshotReads)
	}
	if fingerprint(t, store) != before {
		t.Fatal("the store changed")
	}
}

// Many snapshots: still one snapshot read (the newest usable one).
func TestDryRun_ManySnapshots_ReadsOne(t *testing.T) {
	store := normalCityStore(t)
	ids, _ := store.ListSnapshots(context.Background(), ruleTestCity)
	data, err := store.GetSnapshot(context.Background(), ruleTestCity, ids[len(ids)-1])
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := store.PutSnapshot(context.Background(), ruleTestCity, data); err != nil {
			t.Fatal(err)
		}
	}
	ro := NewDryRunStore(store)
	if err := dryRun(t, ro, ruleTestSeed); err != nil {
		t.Fatal(err)
	}
	if c := ro.Counts(); c.SnapshotReads != 1 || c.JournalReads != 1 {
		t.Fatalf("reads journal=%d snapshot=%d with 6 snapshots, want 1/1", c.JournalReads, c.SnapshotReads)
	}
}

// Every mutating method is refused with the sentinel, counted, and changes
// nothing in the wrapped store.
func TestDryRunStore_RefusesEveryWrite(t *testing.T) {
	ctx := context.Background()
	store := normalCityStore(t)
	before := fingerprint(t, store)
	ro := NewDryRunStore(store)
	ids, _ := store.ListSnapshots(ctx, ruleTestCity)
	if err := ro.AppendJournal(ctx, ruleTestCity, []byte("x")); !errors.Is(err, errDryRunWriteRefused) {
		t.Fatalf("AppendJournal: %v", err)
	}
	if _, err := ro.PutSnapshot(ctx, ruleTestCity, []byte("x")); !errors.Is(err, errDryRunWriteRefused) {
		t.Fatalf("PutSnapshot: %v", err)
	}
	if err := ro.DeleteSnapshot(ctx, ruleTestCity, ids[0]); !errors.Is(err, errDryRunWriteRefused) {
		t.Fatalf("DeleteSnapshot: %v", err)
	}
	if _, err := ro.SetGameModeIfAbsent(ctx, ruleTestCity, "real"); !errors.Is(err, errDryRunWriteRefused) {
		t.Fatalf("SetGameModeIfAbsent: %v", err)
	}
	if err := ro.SetGameModeEpoch(ctx, ruleTestCity); !errors.Is(err, errDryRunWriteRefused) {
		t.Fatalf("SetGameModeEpoch: %v", err)
	}
	// The one simulated write: counted, reports the seed, writes nothing.
	if got, err := ro.SetWorldSeedIfAbsent(ctx, ruleTestCity, 7); err != nil || got != 7 {
		t.Fatalf("SetWorldSeedIfAbsent: %v %v", got, err)
	}
	if n := len(ro.Counts().Writes); n != 6 {
		t.Fatalf("%d methods counted, want 6: %v", n, ro.Counts().Writes)
	}
	if fingerprint(t, store) != before {
		t.Fatal("a refused write reached the wrapped store")
	}
}

// A city that has history but whose seed was never recorded (pre-BUG-488) and
// whose genesis replay hits a retired version: the real wake stamps the seed
// in Wire and then refuses MET-G5611; the dry run must refuse too, not stop at
// its own refused seed stamp and pass.
func TestDryRun_NeverStampedSeedCity_RetiredGenesis_StillRefuses(t *testing.T) {
	rules.SetCurrentForTest(t, 1)
	src := persist.NewMemStore()
	rc := newRuleCity(t, src, 0)
	rc.sessionA(t)              // genesis city, no snapshot, frames at version 0
	store := cloneStore(t, src) // the journal only: no snapshot, and no seed of record
	if _, ok, _ := store.WorldSeed(context.Background(), ruleTestCity); ok {
		t.Fatal("setup: the seed was recorded")
	}
	rules.SetOldestSupportedForTest(t, 1)
	ro := NewDryRunStore(store)
	err := dryRun(t, ro, ruleTestSeed)
	if codeOfErr(t, err) != rules.ErrSavedUnderRetiredRules {
		t.Fatalf("dry run = %v, want MET-G5611", err)
	}
	if got := ro.Counts().Writes["SetWorldSeedIfAbsent"]; got != 1 {
		t.Fatalf("seed stamp attempts %d, want 1 (simulated, not written)", got)
	}
}

// Nothing is left behind: no entry in the OS temp folder, no goroutine.
func TestDryRun_LeavesNoTempFilesAndNoGoroutines(t *testing.T) {
	store := normalCityStore(t)
	_ = dryRun(t, NewDryRunStore(store), ruleTestSeed) // warm lazy package state first
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)
	t.Setenv("TMP", scratch)
	t.Setenv("TEMP", scratch)
	coastal := quietCoastal(t) // built before the baseline: its own state is not the dry run's
	_ = coastal
	base := runtime.NumGoroutine()
	for i := 0; i < 3; i++ {
		if err := dryRun(t, NewDryRunStore(store), ruleTestSeed); err != nil {
			t.Fatal(err)
		}
	}
	left, err := os.ReadDir(scratch)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 0 {
		t.Fatalf("%d entries left in the temp folder, first %s", len(left), left[0].Name())
	}
	deadline := time.Now().Add(20 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		runtime.Gosched()
	}
	if n := runtime.NumGoroutine(); n > base {
		buf := make([]byte, 1<<16)
		buf = buf[:runtime.Stack(buf, true)]
		t.Fatalf("%d goroutines after three dry runs, %d before:\n%s", n, base, buf)
	}
}

// A non-rule failure (corrupt journal) is not the pre-check's to report: it
// returns nil and the real restore reports its own error.
func TestDryRun_CorruptJournal_ReturnsNil_RealRestoreReportsItsOwn(t *testing.T) {
	ctx := context.Background()
	store := normalCityStore(t)
	if err := store.AppendJournal(ctx, ruleTestCity, []byte("this is not a command frame")); err != nil {
		t.Fatal(err)
	}
	if err := dryRun(t, NewDryRunStore(store), ruleTestSeed); err != nil {
		t.Fatalf("pre-check = %v, want nil for a non-rule failure", err)
	}
	real := restoreErr(t, store)
	if real == nil {
		t.Fatal("the real restore accepted a corrupt journal")
	}
	if IsRuleVersionRefusal(real) {
		t.Fatalf("restore error is a rule refusal: %v", real)
	}
}
