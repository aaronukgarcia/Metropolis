package main

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// FEAT-1972079936 Phase 2 inc1 — independent Destructive-round regression tests
// (attacker != author, GR#23). These close two coverage gaps the builder's own
// suite left open, each mutation-proven during the round:
//
//   - The single-city-per-key barrier: the builder's
//     TestCityHost_ConcurrentSameKeyYieldsOne launches its goroutines without a
//     common release point, so they stagger and it PASSES even when the
//     check-and-claim is made non-atomic (lock dropped between the existence
//     check and the claim). TestAttack_SameKeyBarrier_OneEngine below releases
//     all goroutines from a single closed channel, maximising the TOCTOU window,
//     and reliably reddens under that mutation.
//   - The double-append guard on the CityHost path: the builder's
//     TestCityHost_RestartDurability does ONE restart and checks only the digest,
//     which stays correct even with the guard disabled (rehydrate replays the
//     journal it READ exactly once; the on-disk double-append only shows up on a
//     SECOND restart or via a frame count). TestAttack_CityHostRestartTwice_
//     NoGrowth restarts twice through the host AND asserts the journal frame
//     count never grows, so a guard regression on the CityHost path is caught
//     here as well as on inc4's setUpPersistence path.

// discardLog is a quiet io.Writer for the rehydrate lines wireAndRehydrate emits.
type discardLog struct{}

func (discardLog) Write(p []byte) (int, error) { return len(p), nil }

// TestAttack_SameKeyBarrier_OneEngine (AC-2/6): N goroutines released
// simultaneously onto the SAME key must all receive the identical *runningCity,
// with exactly one city registered. Reliably reddens if the barrier's
// check-and-claim is not atomic under the host lock.
func TestAttack_SameKeyBarrier_OneEngine(t *testing.T) {
	h := newTestHost(t, "")
	ctx := context.Background()
	key := persist.CityKey{TenantID: "local", CityID: "hot"}

	const n = 64
	results := make([]*runningCity, n)
	var wg sync.WaitGroup
	wg.Add(n)
	release := make(chan struct{})
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			<-release
			rc, err := h.GetOrCreate(ctx, key)
			if err != nil {
				t.Errorf("goroutine %d: GetOrCreate error: %v", i, err)
				return
			}
			results[i] = rc
		}(i)
	}
	close(release)
	wg.Wait()

	distinct := map[*runningCity]bool{}
	for i := 0; i < n; i++ {
		if results[i] == nil {
			t.Fatalf("goroutine %d got a nil runningCity", i)
		}
		distinct[results[i]] = true
	}
	if len(distinct) != 1 {
		t.Fatalf("same-key GetOrCreate handed out %d distinct runningCity pointers, want exactly 1 (a second engine was built for one key)", len(distinct))
	}
	if c := cityCount(h); c != 1 {
		t.Fatalf("concurrent same-key GetOrCreate registered %d cities, want exactly 1", c)
	}
}

// TestAttack_ConcurrentCorruptNoPoison (AC-2 failure path): N goroutines racing
// GetOrCreate on a key whose journal is corrupt must ALL get an error, leave the
// key unregistered (no half-built city), and never poison the host — a healthy
// key still builds afterwards.
func TestAttack_ConcurrentCorruptNoPoison(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := persist.CityKey{TenantID: "local", CityID: "poison"}
	if err := disk.AppendJournal(ctx, corrupt, []byte("{garbage frame")); err != nil {
		t.Fatal(err)
	}

	h := newTestHost(t, dir)

	const n = 24
	var errCount int64
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if _, e := h.GetOrCreate(ctx, corrupt); e != nil {
				atomic.AddInt64(&errCount, 1)
			}
		}()
	}
	wg.Wait()

	if errCount != n {
		t.Fatalf("only %d of %d concurrent corrupt GetOrCreate calls returned an error", errCount, n)
	}
	if hasCity(h, corrupt) {
		t.Fatal("a corrupt/half-built city was left registered after the concurrent failures")
	}
	if _, e := h.GetOrCreate(ctx, persist.CityKey{TenantID: "local", CityID: "healthy"}); e != nil {
		t.Fatalf("a healthy city failed to build after the corrupt storm (host poisoned): %v", e)
	}
}

// TestAttack_ShutdownRacesGetOrCreate (AC-3): Shutdown concurrent with
// GetOrCreate on the same key, many rounds, must never panic (no double-stop, no
// use-after-close), and a follow-up Shutdown must remain a clean no-op.
func TestAttack_ShutdownRacesGetOrCreate(t *testing.T) {
	h := newTestHost(t, "")
	ctx := context.Background()
	for round := 0; round < 40; round++ {
		key := persist.CityKey{TenantID: "local", CityID: fmt.Sprintf("r%d", round)}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _, _ = h.GetOrCreate(ctx, key) }()
		go func() { defer wg.Done(); _ = h.Shutdown(key) }()
		wg.Wait()
		if err := h.Shutdown(key); err != nil {
			t.Fatalf("round %d follow-up Shutdown was not a clean no-op: %v", round, err)
		}
	}
}

// cityJournalFrames reads how many frames a city's on-disk journal holds — the
// direct measure a double-append would grow across restarts.
func cityJournalFrames(t *testing.T, dir string, key persist.CityKey) int {
	t.Helper()
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := disk.ReadJournal(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	return len(frames)
}

// TestAttack_CityHostRestartTwice_NoGrowth (AC-5 + non-negotiable double-append
// guard, via the CITYHOST path): a city persisted by one host, rehydrated by a
// second and then a third, must reach the same digest AND leave the on-disk
// journal frame count unchanged across BOTH restarts. The second restart is the
// one that exposes a guard regression a single-restart digest check misses.
func TestAttack_CityHostRestartTwice_NoGrowth(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	key := persist.CityKey{TenantID: "local", CityID: "rt2"}

	h1, err := NewCityHost(dir, hostTickDisabled)
	if err != nil {
		t.Fatal(err)
	}
	h1.engineOpts = testEngineOpts()
	h1.logw = discardLog{}
	rc, err := h1.GetOrCreate(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	submitAll(t, rc.Engine(), rtCommands())
	want := rc.Composition().StateDigest()
	if err := h1.Close(); err != nil {
		t.Fatal(err)
	}
	frames := cityJournalFrames(t, dir, key)
	if frames == 0 {
		t.Fatal("no journal frames written — the round-trip is not meaningful")
	}

	for i, label := range []string{"restart#1", "restart#2"} {
		h := newTestHost(t, dir)
		rcR, err := h.GetOrCreate(ctx, key)
		if err != nil {
			t.Fatalf("%s GetOrCreate: %v", label, err)
		}
		if got := rcR.Composition().StateDigest(); got != want {
			t.Fatalf("%s (#%d) digest diverged: %x != %x (double-append / lossy replay via CityHost?)", label, i+1, got, want)
		}
		if f := cityJournalFrames(t, dir, key); f != frames {
			t.Fatalf("%s grew the journal via CityHost: %d frames vs %d (double-append)", label, f, frames)
		}
		_ = h.Close()
	}
}

// TestAttack_CollidingSeedStillIsolated: seedForCity's single-\x00-separator
// concatenation is NOT injective when a field itself contains \x00 —
// {Tenant:"a",City:"\x00b"} and {Tenant:"a\x00",City:"b"} both hash "a\x00\x00b"
// and so derive the SAME world seed (documented finding for the author). This
// test proves that even under that seed collision, ISOLATION still holds:
// DiskStore keys each field with its own SHA-256, so the two cities keep
// separate engines and separate journals and never leak into one another.
func TestAttack_CollidingSeedStillIsolated(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	kA := persist.CityKey{TenantID: "a", CityID: "\x00b"}
	kB := persist.CityKey{TenantID: "a\x00", CityID: "b"}
	if seedForCity(kA) != seedForCity(kB) {
		t.Skip("keys no longer collide under seedForCity; the premise is gone (seed derivation was made injective)")
	}

	h := newTestHost(t, dir)
	rcA, err := h.GetOrCreate(ctx, kA)
	if err != nil {
		t.Fatal(err)
	}
	rcB, err := h.GetOrCreate(ctx, kB)
	if err != nil {
		t.Fatal(err)
	}
	if rcA == rcB {
		t.Fatal("colliding-seed keys returned the SAME runningCity — isolation broken")
	}

	submitAll(t, rcA.Engine(), rtCommands())
	submitAll(t, rcB.Engine(), divergentSeq())
	if rcA.Composition().StateDigest() == rcB.Composition().StateDigest() {
		t.Fatal("colliding-seed cities produced identical digests after divergent command streams — state leaked between them")
	}

	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	fa, err := disk.ReadJournal(ctx, kA)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := disk.ReadJournal(ctx, kB)
	if err != nil {
		t.Fatal(err)
	}
	if len(fa) == 0 || len(fb) == 0 {
		t.Fatalf("expected both colliding-seed cities to keep populated, separate journals; got A=%d B=%d frames", len(fa), len(fb))
	}
}
