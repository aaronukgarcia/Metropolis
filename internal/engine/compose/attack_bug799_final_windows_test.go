//go:build windows

package compose

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// BUG-799 final round (opus-round-bug799-r5) attack tests for part (c): only a
// decode failure (errClaimTorn) may make an unreadable claim stealable; any
// other read error under the takeover lock must make the reclaimer back off.

// finalOpenDenyRead opens path with share mode FILE_SHARE_DELETE only, so any
// other GENERIC_READ open fails ERROR_SHARING_VIOLATION while DeleteFile still
// succeeds. It stands in for any third party (scanner, backup agent, indexer)
// or filesystem state that makes a FRESH claim unreadable for a while without
// making it torn.
func finalOpenDenyRead(t *testing.T, path string) windows.Handle {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		t.Fatalf("deny-read open: %v", err)
	}
	return h
}

// Attack (c)-1, full claimPageDir path: R observes a TORN claim, then (while
// R is still inside that read) a real rival takeover W replaces it with W's
// fresh Reclaimed claim, which then becomes unreadable with a NON-decode error
// when R re-reads it under the lock. R must back off: W stays the only holder.
// Without part (c) R treats the unreadable fresh claim like a torn one,
// removes it and creates its own: two holders.
func TestBUG799Final_UnreadableFreshClaimAfterTornNotStolen(t *testing.T) {
	dir := t.TempDir()
	claimPath := filepath.Join(dir, pageClaimFileName)
	if err := os.WriteFile(claimPath, []byte("{torn"), 0o644); err != nil {
		t.Fatal(err)
	}

	var w *pageDirClaim
	var wErr error
	var denyH windows.Handle
	var calls atomic.Int32
	hook := func(string) {
		if calls.Add(1) != 1 {
			return
		}
		// R holds the torn file open. Run the rival takeover W to completion.
		w, wErr = claimPageDir(dir, rr764City, true, "final-W")
		if wErr == nil {
			denyH = finalOpenDenyRead(t, claimPath)
		}
	}
	claimReadOpenHook.Store(&hook)
	t.Cleanup(func() { claimReadOpenHook.Store(nil) })

	r, rErr := claimPageDir(dir, rr764City, true, "final-R")
	claimReadOpenHook.Store(nil)
	if denyH != 0 {
		_ = windows.CloseHandle(denyH)
	}
	if wErr != nil {
		t.Fatalf("setup: rival takeover W failed: %v", wErr)
	}
	disk, derr := readPageClaim(claimPath)
	if rErr == nil {
		t.Fatalf("FINDING: TWO HOLDERS - W holds %+v and R also won %+v (disk %+v, err %v)", w.record, r.record, disk, derr)
	}
	if derr != nil || disk != w.record {
		t.Fatalf("W's claim not intact on disk: %+v err %v (W %+v)", disk, derr, w.record)
	}
	t.Logf("R refused (expected): %v", rErr)
	_ = os.Remove(claimPath)
}

// Attack (c)-2, liveness: a claim that is PERMANENTLY unreadable with a
// non-decode error (here: claim.pid is a directory) must give a bounded,
// loud refusal on every opener, never a hang, and must never delete it.
func TestBUG799Final_PermanentlyUnreadableClaimBoundedRefusal(t *testing.T) {
	dir := t.TempDir()
	claimPath := filepath.Join(dir, pageClaimFileName)
	if err := os.Mkdir(claimPath, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, reclaim := range []bool{false, true} {
		start := time.Now()
		c, err := claimPageDir(dir, rr764City, reclaim, "final-perm")
		el := time.Since(start)
		if err == nil {
			c.Release()
			t.Fatalf("reclaim=%v: won over an unreadable non-file claim", reclaim)
		}
		if el > 5*time.Second {
			t.Errorf("reclaim=%v: refusal took %v (unbounded?)", reclaim, el)
		}
		t.Logf("reclaim=%v refused in %v: %v", reclaim, el, err)
	}
	if fi, err := os.Stat(claimPath); err != nil || !fi.IsDir() {
		t.Fatalf("unreadable claim was removed/replaced: %v", err)
	}
}

// Attack (c)-2b, liveness on the READ path: a stale torn claim held deny-read
// by a wedged third party for as long as it likes. Every reclaim attempt must
// refuse in bounded time without deleting it, and the very first attempt
// after the handle closes must win (no lasting zero-holder state).
func TestBUG799Final_WedgedUnreadableClaimRefusesThenRecovers(t *testing.T) {
	dir := t.TempDir()
	claimPath := filepath.Join(dir, pageClaimFileName)
	if err := os.WriteFile(claimPath, []byte("{torn"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := finalOpenDenyRead(t, claimPath)
	for i := 0; i < 3; i++ {
		start := time.Now()
		c, err := claimPageDir(dir, rr764City, true, "final-wedged")
		el := time.Since(start)
		if err == nil {
			c.Release()
			_ = windows.CloseHandle(h)
			t.Fatalf("attempt %d: reclaim WON over a claim it could not read (non-decode error)", i)
		}
		if el > 5*time.Second {
			t.Errorf("attempt %d: refusal took %v", i, el)
		}
		t.Logf("attempt %d refused in %v: %v", i, el, err)
	}
	_ = windows.CloseHandle(h)
	if b, err := os.ReadFile(claimPath); err != nil || string(b) != "{torn" {
		t.Fatalf("wedged claim was touched: %q %v", b, err)
	}
	c, err := claimPageDir(dir, rr764City, true, "final-wedged-after")
	if err != nil {
		t.Fatalf("FINDING: still no holder after the wedge cleared: %v", err)
	}
	c.Release()
}

// Attack (c)-3, liveness: a STALE torn claim that is only TRANSIENTLY
// unreadable (a scanner holds it deny-read for 100ms) must still be
// reclaimable: the read retry outlasts the transient window, so (c) does not
// turn a short scanner open into a refusal.
func TestBUG799Final_TransientUnreadableTornStillReclaims(t *testing.T) {
	dir := t.TempDir()
	claimPath := filepath.Join(dir, pageClaimFileName)
	if err := os.WriteFile(claimPath, []byte("{torn"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := finalOpenDenyRead(t, claimPath)
	go func() { time.Sleep(100 * time.Millisecond); _ = windows.CloseHandle(h) }()
	c, err := claimPageDir(dir, rr764City, true, "final-transient")
	if err != nil {
		t.Fatalf("reclaim of a stale torn claim refused after a 100ms transient deny-read: %v", err)
	}
	disk, derr := readPageClaim(claimPath)
	if derr != nil || disk != c.record {
		t.Fatalf("disk %+v err %v != %+v", disk, derr, c.record)
	}
	c.Release()
	if _, err := os.Stat(claimPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("release left claim: %v", err)
	}
}

// Attack F1 (create->write window): tryCreate is O_EXCL create, THEN Write,
// THEN Close. Between create and write, a live claimant's claim.pid exists and
// is EMPTY, which readPageClaim reports as errClaimTorn. A reclaim=true opener
// that reads it in that window treats it as torn => stealable, and removes the
// live claimant's claim. This test plays the claimant P with exactly
// tryCreate's syscalls and a 50ms descheduling pause between create and
// write; P's tryCreate would return nil (P holds). P is a reclaim=true
// opener's FIRST create (Reclaimed from first create, the r1 gap-landing
// guard), so once written it is a fresh live Reclaimed claim that R must never
// steal. Exactly one holder is required: R must NOT win.
func TestBUG799Final_TornWindowOfLiveCreateNotStolen(t *testing.T) {
	dir := t.TempDir()
	claimPath := filepath.Join(dir, pageClaimFileName)
	pRec := pageClaimRecord{PID: os.Getpid(), TenantID: rr764City.TenantID, CityID: rr764City.CityID, Epoch: claimNow().UnixNano(), Nonce: newClaimNonce(), Reclaimed: true}

	f, err := os.OpenFile(claimPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	pDone := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond) // P descheduled between create and write
		data, _ := json.Marshal(pRec)
		_, werr := f.Write(data)
		cerr := f.Close()
		if werr != nil {
			pDone <- werr
			return
		}
		pDone <- cerr
	}()

	r, rErr := claimPageDir(dir, rr764City, true, "final-tornwindow")
	pErr := <-pDone
	if pErr != nil {
		t.Fatalf("setup: P's create failed: %v", pErr)
	}
	// P's tryCreate returned nil: P holds and will Wire.
	if rErr == nil {
		disk, derr := readPageClaim(claimPath)
		t.Fatalf("FINDING: TWO HOLDERS - live claimant P %+v (its create succeeded) AND reclaimer R %+v; disk %+v err %v", pRec, r.record, disk, derr)
	}
	t.Logf("R refused: %v", rErr)
	_ = os.Remove(claimPath)
}

// Attack F1-natural: the same window with REAL claimPageDir openers only, on
// an EMPTY directory (no stale claim at all): plain openers and reclaimers
// start on one barrier. RECLAIMERS ONLY (a reclaim=true opener stealing a
// live PLAIN claim is by design, so plain openers are excluded): every
// reclaimer's claim is Reclaimed from first create, so any >1 holder is a bug.
func TestBUG799Final_EmptyDirRaceNaturalTornSteal(t *testing.T) {
	doubles := 0
	const iters = 300
	for i := 0; i < iters; i++ {
		dir := t.TempDir()
		held := r1RaceOnce(dir, 8, 0)
		if len(held) > 1 {
			doubles++
		}
		_ = os.Remove(filepath.Join(dir, pageClaimFileName))
	}
	t.Logf("empty-dir race: %d/%d iterations with >1 holder", doubles, iters)
	if doubles > 0 {
		t.Errorf("FINDING: %d/%d empty-dir races produced TWO+ holders", doubles, iters)
	}
}

func r1RaceOnce(dir string, reclaimers, plain int) []*pageDirClaim {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var held []*pageDirClaim
	start := make(chan struct{})
	for i := 0; i < reclaimers+plain; i++ {
		reclaim := i < reclaimers
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if c, err := claimPageDir(dir, rr764City, reclaim, "final-nat"); err == nil {
				mu.Lock()
				held = append(held, c)
				mu.Unlock()
			}
		}()
	}
	close(start)
	wg.Wait()
	return held
}
