package compose

// BUG-799 round r1 (independent attacker, opus-round-r1-bug799): direct
// claimPageDir-level attacks on the takeover-lock reclaim protocol. These
// call claimPageDir itself (no Wire) so hundreds of contended iterations fit
// in seconds.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// r1Race starts reclaimers (reclaim=true) and plain openers (reclaim=false)
// on one barrier and returns the claims that came back nil-error.
func r1Race(t *testing.T, dir string, reclaimers, plain int) []*pageDirClaim {
	t.Helper()
	var wg, ready sync.WaitGroup
	var mu sync.Mutex
	var held []*pageDirClaim
	start := make(chan struct{})
	// reclaimersLeft counts takeovers still running. The remove->create gap a
	// plain opener is hunting exists only while a takeover runs, so spinning
	// after the last one finishes can only meet a held claim (BUG-1215: on
	// Windows every such wasted attempt is several filesystem calls, and
	// 6 spinners x 400 attempts x 40 iterations took 7-22 minutes).
	var reclaimersLeft atomic.Int32
	reclaimersLeft.Store(int32(reclaimers))
	launch := func(reclaim bool) {
		wg.Add(1)
		ready.Add(1)
		go func() {
			defer wg.Done()
			if reclaim {
				defer reclaimersLeft.Add(-1)
			}
			ready.Done()
			<-start
			// Plain openers SPIN (up to 400 attempts) so one can actually
			// land inside a takeover's remove->create gap; a refusal is the
			// expected outcome of every attempt outside the gap.
			attempts := 1
			if !reclaim {
				attempts = 400
			}
			var c *pageDirClaim
			var err error
			for a := 0; a < attempts; a++ {
				if c, err = claimPageDir(dir, rr764City, reclaim, "r1"); err == nil {
					break
				}
				// Always make the first attempt; after that stop once no
				// takeover is left to race (see reclaimersLeft).
				if !reclaim && reclaimersLeft.Load() == 0 {
					break
				}
			}
			if err != nil {
				// BUG-799 r3 FLIP: a loser that met the held takeover lock is
				// refused with the distinct "takeover in progress" code.
				if code := rr764CodeSafe(err); code != ErrCitizenPagingDirectoryClaimed && code != ErrCitizenPagingTakeoverInProgress {
					t.Errorf("FINDING: loser (reclaim=%v) refused with %s (%v), want %s or %s", reclaim, code, err, ErrCitizenPagingDirectoryClaimed, ErrCitizenPagingTakeoverInProgress)
				}
				return
			}
			mu.Lock()
			held = append(held, c)
			mu.Unlock()
		}()
	}
	for i := 0; i < reclaimers; i++ {
		launch(true)
	}
	for i := 0; i < plain; i++ {
		launch(false)
	}
	ready.Wait()
	close(start)
	wg.Wait()
	return held
}

// r1PlantStale writes a non-Reclaimed claim for a pid that cannot be alive
// (stealable by any reclaim=true opener on every GOOS).
func r1PlantClaim(t *testing.T, dir string, rec pageClaimRecord) {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, pageClaimFileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// Attack 2: plain openers race a takeover's remove->create gap. Whoever
// returns a claim must be the ONLY one, and its record must be what is on
// disk (both sides agree on the holder).
func TestBUG799R1_PlainOpenerVsTakeoverGapAgreement(t *testing.T) {
	gapWins := 0
	for iter := 0; iter < 40; iter++ {
		dir := t.TempDir()
		r1PlantClaim(t, dir, pageClaimRecord{PID: 1 << 30, TenantID: rr764City.TenantID, CityID: rr764City.CityID, Epoch: 1})
		held := r1Race(t, dir, 3, 6)
		if len(held) != 1 {
			t.Fatalf("FINDING iter %d: %d holders (want exactly 1)", iter, len(held))
		}
		disk, err := readPageClaim(filepath.Join(dir, pageClaimFileName))
		if err != nil || disk != held[0].record {
			t.Fatalf("FINDING iter %d: holder record %+v != disk %+v (err %v)", iter, held[0].record, disk, err)
		}
		if !disk.Reclaimed {
			gapWins++
		}
		// BUG-799 r3 FLIP: the lock FILE persists by design (never deleted by
		// the protocol); what must not be left behind is the LOCK itself.
		lf, oerr := os.OpenFile(filepath.Join(dir, pageClaimFileName+".reclaim"), os.O_CREATE|os.O_RDWR, 0o644)
		if oerr != nil {
			t.Fatal(oerr)
		}
		got, lerr := tryLockFile(lf)
		if lerr != nil || !got {
			t.Fatalf("FINDING iter %d: takeover lock left held (held=%v err=%v)", iter, got, lerr)
		}
		_ = unlockFile(lf)
		_ = lf.Close()
	}
	t.Logf("plain opener landed in the takeover gap %d/40 times (each one agreed on by both sides)", gapWins)
}

// Attack 3 (BUG-799 r3 FLIP): a stale lock FILE is no longer a concept -- the
// takeover lock is an OS advisory lock the kernel drops on process death, and
// the protocol never deletes the file. A leftover, ancient lock file (what a
// crash used to leave) must therefore be inert: N concurrent reclaimers of one
// stale CLAIM yield EXACTLY one holder, every time.
func TestBUG799R1_StaleLockDoubleBreak(t *testing.T) {
	const iters = 300
	for iter := 0; iter < iters; iter++ {
		dir := t.TempDir()
		r1PlantClaim(t, dir, pageClaimRecord{PID: 1 << 30, TenantID: rr764City.TenantID, CityID: rr764City.CityID, Epoch: 1})
		lock := filepath.Join(dir, pageClaimFileName+".reclaim")
		if err := os.WriteFile(lock, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		old := time.Now().Add(-2 * time.Hour)
		if err := os.Chtimes(lock, old, old); err != nil {
			t.Fatal(err)
		}
		held := r1Race(t, dir, 8, 0)
		if len(held) != 1 {
			t.Fatalf("FINDING iter %d: %d winners over a leftover lock file (want exactly 1)", iter, len(held))
		}
	}
}

// Attack 4: a Reclaimed claim left by a takeover winner that then CRASHED
// within reclaimFreshWindow. On GOOS without pid liveness it is unreclaimable
// until the window passes (the old code stole it at once). Documents the
// soft wedge; asserts it is BOUNDED by the window.
func TestBUG799R1_DeadFreshReclaimedHolderWindow(t *testing.T) {
	dir := t.TempDir()
	r1PlantClaim(t, dir, pageClaimRecord{PID: 1 << 30, TenantID: rr764City.TenantID, CityID: rr764City.CityID, Epoch: time.Now().UnixNano(), Reclaimed: true})
	_, err := claimPageDir(dir, rr764City, true, "r1")
	if pidLivenessKnown {
		if err != nil {
			t.Fatalf("linux: dead-pid fresh reclaimed claim must be stealable at once, got %v", err)
		}
		return
	}
	if rr764CodeSafe(err) != ErrCitizenPagingDirectoryClaimed {
		t.Fatalf("expected the documented %s soft wedge on %s, got %v", ErrCitizenPagingDirectoryClaimed, runtime.GOOS, err)
	}
	realNow := claimNow
	t.Cleanup(func() { claimNow = realNow })
	claimNow = func() time.Time { return realNow().Add(reclaimFreshWindow + time.Second) }
	if _, err := claimPageDir(dir, rr764City, true, "r1"); err != nil {
		t.Fatalf("FINDING: wedge outlived reclaimFreshWindow: %v", err)
	}
}

// Attack 4b: a Reclaimed claim whose Epoch is in the FUTURE (wall clock
// stepped backwards after the claim, or a clock-skewed writer) has a negative
// age, which counts as fresh: the wedge is no longer bounded by the window.
func TestBUG799R1_FutureEpochReclaimedClaimWedge(t *testing.T) {
	if pidLivenessKnown {
		t.Skip("liveness known: dead pid is stolen regardless of age")
	}
	dir := t.TempDir()
	r1PlantClaim(t, dir, pageClaimRecord{PID: 1 << 30, TenantID: rr764City.TenantID, CityID: rr764City.CityID, Epoch: time.Now().Add(time.Hour).UnixNano(), Reclaimed: true})
	realNow := claimNow
	t.Cleanup(func() { claimNow = realNow })
	claimNow = func() time.Time { return realNow().Add(reclaimFreshWindow + time.Minute) }
	_, err := claimPageDir(dir, rr764City, true, "r1")
	if err != nil {
		t.Logf("FINDING (minor): future-Epoch Reclaimed claim still refused %v past the window: %v", reclaimFreshWindow+time.Minute, rr764CodeSafe(err))
	}
}

// Attack 5: a claim written by the PRE-BUG-799 code (no nonce/reclaimed keys)
// decodes as Reclaimed=false and is stolen by an operator Reclaim even when
// its epoch is fresh -- never mis-classified as a permanently fresh reclaim.
func TestBUG799R1_LegacyClaimFormatStealable(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"pid":` + fmt.Sprint(os.Getpid()) + `,"tenant_id":"` + rr764City.TenantID + `","city_id":"` + rr764City.CityID + `","epoch":` + fmt.Sprint(time.Now().UnixNano()) + `}`
	if err := os.WriteFile(filepath.Join(dir, pageClaimFileName), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := readPageClaim(filepath.Join(dir, pageClaimFileName))
	if err != nil || rec.Reclaimed || rec.Nonce != "" {
		t.Fatalf("legacy decode: %+v err=%v", rec, err)
	}
	if _, err := claimPageDir(dir, rr764City, false, "r1"); rr764CodeSafe(err) != ErrCitizenPagingDirectoryClaimed {
		t.Fatalf("FINDING: plain opener against a live legacy claim: %v", err)
	}
	c, err := claimPageDir(dir, rr764City, true, "r1")
	if err != nil {
		t.Fatalf("FINDING: operator Reclaim refused a fresh legacy claim: %v", err)
	}
	if !c.record.Reclaimed {
		t.Fatal("takeover claim not marked Reclaimed")
	}
}

// Attack 7: a persistent non-IsExist create error is retried transientRetries
// times then surfaces as the config error (MET-G817), not a refusal; measure
// how long the sleep loop holds the caller.
func TestBUG799R1_PersistentCreateErrorNotMaskedAsRefusal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does-not-exist")
	t0 := time.Now()
	_, err := claimPageDir(dir, rr764City, true, "r1")
	el := time.Since(t0)
	t.Logf("persistent create error surfaced after %v as %s", el, rr764CodeSafe(err))
	if rr764CodeSafe(err) != ErrCitizenPagingConfigInvalid {
		t.Fatalf("FINDING: persistent create error surfaced as %v, want %s", err, ErrCitizenPagingConfigInvalid)
	}
	if el > 10*time.Second {
		t.Fatalf("FINDING: retry loop held the caller %v", el)
	}
}
