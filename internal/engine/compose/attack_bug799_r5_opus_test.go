package compose

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// BUG-799 r5 destructive round (opus-round-bug799-r5). Attacks the torn-claim
// refinement (can a live legacy write's prefix classify as corrupt and be
// stolen at once?) and the lock-on-first-create path under mixed contention
// (two holders, or zero holders, under normal contention?).

// Every strict prefix of a real legacy (json.Marshal, one Write) claim record
// must classify as in-flight, including records whose strings carry escapes,
// multi-byte UTF-8 and HTML-escaped runes; otherwise a reclaim would treat a
// live legacy writer's half-written claim as corrupt and steal it at once.
func TestAttackBUG799R5_EveryPrefixIsInFlight(t *testing.T) {
	recs := []pageClaimRecord{
		{PID: 1234567, TenantID: rr764City.TenantID, CityID: rr764City.CityID, Epoch: 1730000000123456789, Nonce: "0123456789abcdef"},
		{PID: 7, TenantID: "t\"\\ü\u2028<>&\x01", CityID: "城市-🏙", Epoch: -5, Nonce: "n", Reclaimed: true},
		{},
	}
	for ri, r := range recs {
		data, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		for _, full := range [][]byte{data, append(append([]byte{}, data...), '\n')} {
			for n := 0; n < len(data); n++ { // strict prefixes of the value only
				if !claimBytesInFlight(full[:n]) {
					t.Errorf("FINDING: rec %d prefix %d/%d %q classified corrupt (stolen at once)", ri, n, len(data), full[:n])
				}
			}
			var probe pageClaimRecord
			if err := json.Unmarshal(full, &probe); err != nil || probe != r {
				t.Fatalf("rec %d full record does not round-trip: %v", ri, err)
			}
		}
	}
	// Probe only (not a pass/fail): a zero-extended file (size grown before
	// data lands) is classified corrupt, i.e. stale at once.
	t.Logf("probe: NUL-filled 40 bytes inFlight=%v; prefix+NULs inFlight=%v",
		claimBytesInFlight(make([]byte, 40)), claimBytesInFlight(append([]byte(`{"pid":1`), make([]byte, 30)...)))
}

// Mixed plain + reclaim openers racing over one STALE (not Reclaimed, old)
// claim: exactly one holder every iteration, the holder matches disk, and the
// bounded lock wait never times out (no MET-G822) under this normal
// contention. Lock-on-first-create means the claim is never absent outside
// the lock, so a plain opener can never land and be stolen.
func TestAttackBUG799R5_MixedContentionExactlyOneHolder(t *testing.T) {
	const iters, openers = 60, 16
	for it := 0; it < iters; it++ {
		dir := t.TempDir()
		claimPath := filepath.Join(dir, pageClaimFileName)
		stale, _ := json.Marshal(pageClaimRecord{PID: 999999, TenantID: rr764City.TenantID, CityID: rr764City.CityID, Epoch: 1, Nonce: "stale"})
		if err := os.WriteFile(claimPath, stale, 0o644); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		var mu sync.Mutex
		var holders []*pageDirClaim
		var g822 int
		for i := 0; i < openers; i++ {
			wg.Add(1)
			go func(reclaim bool) {
				defer wg.Done()
				<-start
				c, err := claimPageDir(dir, rr764City, reclaim, "r5-opus-mixed")
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					holders = append(holders, c)
					return
				}
				if rr764CodeSafe(err) == ErrCitizenPagingTakeoverInProgress {
					g822++
				}
			}(i%2 == 0)
		}
		close(start)
		wg.Wait()
		if len(holders) != 1 {
			t.Fatalf("FINDING iter %d: %d holders (want exactly 1), G822=%d", it, len(holders), g822)
		}
		disk, err := readPageClaim(claimPath)
		if err != nil || disk != holders[0].record {
			t.Fatalf("FINDING iter %d: disk %+v err %v != holder %+v", it, disk, err, holders[0].record)
		}
		if g822 != 0 {
			t.Fatalf("FINDING iter %d: %d openers timed out on the bounded lock wait under normal contention", it, g822)
		}
		holders[0].Release()
	}
}

// Plain openers only, racing on an EMPTY directory: exactly one holder; a
// zero-holder iteration would mean the bounded wait or the Lstat fast path
// refused everyone.
func TestAttackBUG799R5_EmptyDirPlainContentionExactlyOne(t *testing.T) {
	const iters, openers = 60, 16
	for it := 0; it < iters; it++ {
		dir := t.TempDir()
		var wg sync.WaitGroup
		start := make(chan struct{})
		var mu sync.Mutex
		var holders []*pageDirClaim
		var codes = map[string]int{}
		for i := 0; i < openers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				c, err := claimPageDir(dir, rr764City, false, "r5-opus-empty")
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					holders = append(holders, c)
					return
				}
				codes[rr764CodeSafe(err)]++
			}()
		}
		close(start)
		wg.Wait()
		if len(holders) != 1 {
			t.Fatalf("FINDING iter %d: %d holders (want exactly 1), refusals %v", it, len(holders), codes)
		}
		for code := range codes {
			if code != ErrCitizenPagingDirectoryClaimed {
				t.Fatalf("FINDING iter %d: a loser refused with %s, want only %s (%v)", it, code, ErrCitizenPagingDirectoryClaimed, codes)
			}
		}
		holders[0].Release()
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if e.Name() != pageClaimFileName+".reclaim" {
				t.Fatalf("iter %d: residue %q left after release", it, e.Name())
			}
		}
	}
	_ = time.Second
}

// The observed-claim back-off in takeOverClaim is still load-bearing after r5:
// a slow reclaimer R2 judges a genuinely stale claim S, pauses before taking
// the takeover lock, and meanwhile R1 takes S over, R1 closes (Release), and a
// plain opener P creates a fresh claim. When R2 resumes, the claim under the
// lock is P's, which R2 never examined: it must back off. Without the back-off
// R2 steals P's live plain claim (stealable: not Reclaimed) and P and R2 both
// hold. With lock-on-first-create the r1 PlainOpener test no longer reaches
// this, so this deterministic test is what pins the back-off.
func TestAttackBUG799R5_SlowReclaimerMustNotStealPostReleasePlainClaim(t *testing.T) {
	dir := t.TempDir()
	claimPath := filepath.Join(dir, pageClaimFileName)
	stale, _ := json.Marshal(pageClaimRecord{PID: 999999, TenantID: rr764City.TenantID, CityID: rr764City.CityID, Epoch: 1, Nonce: "stale"})
	if err := os.WriteFile(claimPath, stale, 0o644); err != nil {
		t.Fatal(err)
	}
	// Only the FIRST read (R2's observation) pauses; every later read
	// passes straight through (a CAS, not sync.Once: Once would block the
	// other readers until R2 resumed).
	var armed atomic.Bool
	armed.Store(true)
	paused := make(chan struct{})
	resume := make(chan struct{})
	hook := func(string) {
		if armed.CompareAndSwap(true, false) {
			close(paused)
			<-resume
		}
	}
	claimReadOpenHook.Store(&hook)
	t.Cleanup(func() { claimReadOpenHook.Store(nil) })

	type res struct {
		c   *pageDirClaim
		err error
	}
	r2 := make(chan res, 1)
	go func() {
		c, err := claimPageDir(dir, rr764City, true, "r5-opus-slow-r2")
		r2 <- res{c, err}
	}()
	select {
	case <-paused:
	case <-time.After(10 * time.Second):
		t.Fatal("R2 never reached its observation read")
	}
	r1, err := claimPageDir(dir, rr764City, true, "r5-opus-r1")
	if err != nil {
		close(resume)
		t.Fatalf("R1 reclaim over the stale claim failed: %v", err)
	}
	r1.Release()
	p, err := claimPageDir(dir, rr764City, false, "r5-opus-plain")
	if err != nil {
		close(resume)
		t.Fatalf("plain opener after R1's release failed: %v", err)
	}
	close(resume)
	got := <-r2
	if got.err == nil {
		disk, derr := readPageClaim(claimPath)
		got.c.Release()
		p.Release()
		t.Fatalf("FINDING: TWO HOLDERS - plain P %+v and slow reclaimer R2 %+v (disk %+v err %v)", p.record, got.c.record, disk, derr)
	}
	disk, derr := readPageClaim(claimPath)
	if derr != nil || disk != p.record {
		t.Fatalf("P's claim was disturbed: disk %+v err %v want %+v", disk, derr, p.record)
	}
	p.Release()
}
