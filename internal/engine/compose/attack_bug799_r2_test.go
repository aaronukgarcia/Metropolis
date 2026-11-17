package compose

// BUG-799 r3 FLIP of round r2's TestBUG799R2_BreakerDeletesFreshLockTwoHolders.
//
// The r2 attack paused a stale-lock BREAKER inside its re-stat and showed it
// deleting a rival's FRESH lock file, giving two holders. The r3 design has no
// breaker and no stale-lock concept at all: the takeover lock is an OS
// advisory lock (LockFileEx / flock) on a file the protocol never deletes, and
// the kernel releases it on process death. The invariant this test now holds:
//
//	a crash-left takeover lock cannot exist, and a takeover that is paused
//	while HOLDING the lock excludes every other reclaimer -- a rival is
//	refused ("takeover in progress"), never a second holder.
//
// The pause uses the claimNow seam (no production change): stealable's age
// check runs under the lock, so blocking inside that claimNow call holds the
// lock deterministically.

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestBUG799R2_BreakerDeletesFreshLockTwoHolders(t *testing.T) {
	dir := t.TempDir()
	claimPath := filepath.Join(dir, pageClaimFileName)
	lockPath := claimPath + ".reclaim"
	r1PlantClaim(t, dir, pageClaimRecord{PID: 1 << 30, TenantID: rr764City.TenantID, CityID: rr764City.CityID, Epoch: 1})
	// An ancient leftover lock FILE (what a crash used to leave): inert now.
	if err := os.WriteFile(lockPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(lockPath, old, old); err != nil {
		t.Fatal(err)
	}

	realNow := claimNow
	t.Cleanup(func() { claimNow = realNow })
	var calls atomic.Int64
	reached, release := make(chan struct{}), make(chan struct{})
	claimNow = func() time.Time {
		// call 1 = W's record epoch, call 2 = W's stealable age check, which
		// runs UNDER the takeover lock: pause there. Later calls pass.
		if calls.Add(1) == 2 {
			close(reached)
			<-release
		}
		return realNow()
	}

	type result struct {
		c   *pageDirClaim
		err error
	}
	run := func() chan result {
		ch := make(chan result, 1)
		go func() {
			c, err := claimPageDir(dir, rr764City, true, "r3")
			ch <- result{c, err}
		}()
		return ch
	}

	w := run()
	select {
	case <-reached:
	case <-time.After(10 * time.Second):
		t.Fatal("W never reached its under-lock pause point (claimNow call order changed?)")
	}

	// R arrives while W holds the lock, mid-judgement of the stale claim. It
	// must be excluded: refused as "takeover in progress", never a holder.
	rres := <-run()
	if rres.err == nil {
		t.Errorf("FINDING: a rival reclaimer got a claim while W held the takeover lock (two holders)")
	} else if code := rr764CodeSafe(rres.err); code != ErrCitizenPagingTakeoverInProgress {
		t.Errorf("rival refused with %s (%v), want %s", code, rres.err, ErrCitizenPagingTakeoverInProgress)
	}

	close(release)
	wres := <-w
	if wres.err != nil {
		t.Fatalf("W (the lock holder) must win its takeover, got %v", wres.err)
	}
	disk, derr := readPageClaim(claimPath)
	if derr != nil || disk != wres.c.record {
		t.Errorf("disk claim %+v (err %v) != W's record %+v", disk, derr, wres.c.record)
	}
	// The lock file persists (never deleted) but the lock itself is free.
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatalf("lock file must persist after the takeover: %v", err)
	}
	defer func() { _ = lf.Close() }()
	if held, lerr := tryLockFile(lf); lerr != nil || !held {
		t.Errorf("takeover lock still held after W finished (held=%v err=%v)", held, lerr)
	} else {
		_ = unlockFile(lf)
	}
}
