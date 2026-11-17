package compose

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// BUG-799 r4: deterministic repro of the zero-holders liveness defect. A plain
// opener is parked INSIDE its diagnostic read of claim.pid (file open). A
// reclaimer must still be able to take the stale claim over: before the fix the
// open reader blocked DeleteFile (ERROR_SHARING_VIOLATION on Windows), the
// reclaimer burned its retries and refused, the plain opener also refused:
// zero holders. Exactly one holder (the reclaimer) is required.
func TestBUG799R4_OpenReaderDoesNotBlockReclaim(t *testing.T) {
	dir := t.TempDir()
	r1PlantClaim(t, dir, pageClaimRecord{PID: 1 << 30, TenantID: rr764City.TenantID, CityID: rr764City.CityID, Epoch: 1})
	claimPath := filepath.Join(dir, pageClaimFileName)

	parked := make(chan struct{})
	release := make(chan struct{})
	var first atomic.Bool
	hook := func(string) {
		if first.CompareAndSwap(false, true) {
			close(parked)
			<-release
		}
	}
	claimReadOpenHook.Store(&hook)
	t.Cleanup(func() { claimReadOpenHook.Store(nil) })

	plainDone := make(chan error, 1)
	go func() {
		_, err := claimPageDir(dir, rr764City, false, "r4")
		plainDone <- err
	}()
	<-parked // plain opener now holds claim.pid open

	start := time.Now()
	c, err := claimPageDir(dir, rr764City, true, "r4")
	if err != nil {
		close(release)
		<-plainDone
		t.Fatalf("FINDING: reclaimer refused while a plain opener's read held the claim open (zero holders): %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("reclaim took %v", time.Since(start))
	}
	close(release)
	if perr := <-plainDone; perr == nil {
		t.Fatalf("FINDING: plain opener ALSO holds (two holders)")
	}
	disk, derr := readPageClaim(claimPath)
	if derr != nil || disk != c.record {
		t.Fatalf("disk claim %+v (err %v) != reclaimer record %+v", disk, derr, c.record)
	}
	_ = os.Remove(claimPath)
}
