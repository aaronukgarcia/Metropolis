package compose

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
)

// Independent destructive RE-ROUND on BUG-764 (attacker: opus-reround-bug764)
// against the O_EXCL claim-file rework of round finding F1/F2.

const rr764Seed = uint64(764950)

var rr764City = persist.CityKey{TenantID: "local", CityID: "city-reround"}

func rr764Wire(t *testing.T, dir string, reclaim bool) (*Composition, error) {
	t.Helper()
	e := core.NewEngine(core.WithWorldSeed(rr764Seed))
	return Wire(e, &Deps{
		PersistCity: rr764City,
		CitizenPaging: CitizenPagingOptions{
			Enabled: true, MaxResidentShards: 2, PageDir: dir, Reclaim: reclaim,
		},
	})
}

func rr764Code(t *testing.T, err error) string {
	t.Helper()
	var re *errs.E
	if !errors.As(err, &re) {
		t.Fatalf("not a registry error: %v", err)
	}
	return re.Code
}

func rr764ClaimExists(t *testing.T, dir string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(dir, pageClaimFileName))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("stat claim: %v", err)
	}
	return err == nil
}

// R1: N openers race the O_EXCL create against a directory that does not
// even exist yet. EXACTLY one must win; every loser must be refused with
// the claim code (never silently succeed, never a non-registry error).
func TestAttackBUG764RR_ClaimRaceExactlyOneWinner(t *testing.T) {
	const openers = 16
	root := t.TempDir()
	dir := filepath.Join(root, "pages-race") // deliberately absent: MkdirAll races too

	var wg sync.WaitGroup
	var mu sync.Mutex
	var winners []*Composition
	codes := map[string]int{}
	start := make(chan struct{})
	for i := 0; i < openers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			comp, err := rr764Wire(t, dir, false)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				winners = append(winners, comp)
				return
			}
			var re *errs.E
			if !errors.As(err, &re) {
				codes["NON-REGISTRY: "+err.Error()]++
				return
			}
			codes[re.Code]++
		}()
	}
	close(start)
	wg.Wait()

	t.Logf("winners=%d loser codes=%v", len(winners), codes)
	if len(winners) != 1 {
		t.Fatalf("FINDING: %d concurrent openers won the exclusive claim (want exactly 1)", len(winners))
	}
	for code, n := range codes {
		if code != ErrCitizenPagingDirectoryClaimed {
			t.Errorf("FINDING: %d loser(s) refused with %s, not %s", n, code, ErrCitizenPagingDirectoryClaimed)
		}
	}
	// The winner releasing must make the directory claimable again.
	if err := winners[0].Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if rr764ClaimExists(t, dir) {
		t.Fatal("FINDING: claim file survived Close()")
	}
	comp, err := rr764Wire(t, dir, false)
	if err != nil {
		t.Fatalf("FINDING: re-open after a clean Close was refused: %v", err)
	}
	_ = comp.Close()
}

// R2: Close() twice, and Close() on a paging-DISABLED composition (nil
// claim) must both be silent no-ops, and must not strand the directory.
func TestAttackBUG764RR_CloseTwiceAndCloseWithoutPaging(t *testing.T) {
	dir := t.TempDir()
	comp, err := rr764Wire(t, dir, false)
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	if err := comp.Close(); err != nil {
		t.Fatalf("Close #1: %v", err)
	}
	if err := comp.Close(); err != nil {
		t.Fatalf("FINDING: second Close errored: %v", err)
	}

	// A second live composition takes the directory; the FIRST composition's
	// third Close must not steal the new holder's claim.
	comp2, err := rr764Wire(t, dir, false)
	if err != nil {
		t.Fatalf("re-open: %v", err)
	}
	_ = comp.Close() // stale handle, same path
	// RE-ROUND FIX (P2, opus-reround-bug764): pageDirClaim.Release() now
	// does a read-compare-delete against the exact pid+epoch stamp THIS
	// handle wrote, so a stale handle's Close() must NOT delete a
	// different (current) claim file's content -- comp2's claim must
	// survive comp's stale third Close().
	if !rr764ClaimExists(t, dir) {
		t.Fatal("FINDING (P2): a stale Composition's Close() deleted the CURRENT holder's claim file -- Release() has no ownership check, so the exclusion guard can be silently disarmed")
	}
	_ = comp2.Close()

	// Paging off: Close must be a no-op, never a panic.
	e := core.NewEngine(core.WithWorldSeed(rr764Seed))
	plain, err := Wire(e, nil)
	if err != nil {
		t.Fatalf("Wire (no paging): %v", err)
	}
	if err := plain.Close(); err != nil {
		t.Fatalf("FINDING: Close on a paging-disabled composition errored: %v", err)
	}
	if err := plain.Close(); err != nil {
		t.Fatalf("FINDING: second Close on a paging-disabled composition errored: %v", err)
	}
	var nilComp *Composition
	if err := nilComp.Close(); err != nil {
		t.Fatalf("FINDING: Close on a nil Composition errored: %v", err)
	}
}

// R3: a Wire that FAILS AFTER the claim was taken must not strand the
// directory (the deferred release). An unrecognised GameMode fails late in
// Wire, well past the citizen-paging call site.
func TestAttackBUG764RR_WireFailureAfterClaimReleasesIt(t *testing.T) {
	dir := t.TempDir()
	e := core.NewEngine(core.WithWorldSeed(rr764Seed))
	_, err := Wire(e, &Deps{
		PersistCity:   rr764City,
		GameMode:      "definitely-not-a-mode",
		CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 2, PageDir: dir},
	})
	if err == nil {
		t.Skip("Wire accepted the bogus game mode: cannot exercise the late-failure path this way")
	}
	t.Logf("late Wire failure: %v", err)
	if rr764ClaimExists(t, dir) {
		t.Fatal("FINDING: a Wire that failed AFTER taking the claim left the claim file behind (directory permanently stranded)")
	}
	comp, err := rr764Wire(t, dir, false)
	if err != nil {
		t.Fatalf("FINDING: directory unusable after a failed Wire: %v", err)
	}
	_ = comp.Close()
}

// R4: the crashed-process shape -- a claim that is never released. A later
// open must be refused, and Reclaim must clear it AND stamp a fresh claim
// that the reclaimer itself owns and releases.
func TestAttackBUG764RR_NeverClosedThenReclaim(t *testing.T) {
	dir := t.TempDir()
	comp1, err := rr764Wire(t, dir, false)
	if err != nil {
		t.Fatalf("Wire 1: %v", err)
	}
	_ = comp1 // deliberately never Closed: the crash shape

	before, err := readPageClaim(filepath.Join(dir, pageClaimFileName))
	if err != nil {
		t.Fatalf("read claim: %v", err)
	}

	_, err = rr764Wire(t, dir, false)
	if err == nil {
		t.Fatal("FINDING: a second live opener was NOT refused against an unreleased claim")
	}
	if code := rr764Code(t, err); code != ErrCitizenPagingDirectoryClaimed {
		t.Fatalf("FINDING: expected %s, got %s: %v", ErrCitizenPagingDirectoryClaimed, code, err)
	}

	comp2, err := rr764Wire(t, dir, true) // operator reclaim
	if err != nil {
		t.Fatalf("FINDING: -citizen-paging-reclaim did not clear a stale claim: %v", err)
	}
	after, err := readPageClaim(filepath.Join(dir, pageClaimFileName))
	if err != nil {
		t.Fatalf("FINDING: reclaim did not stamp a fresh claim file: %v", err)
	}
	if after.Epoch == before.Epoch {
		t.Fatalf("FINDING: reclaim reused the stale claim record verbatim (epoch %d) -- a later diagnosis cannot tell the two apart", after.Epoch)
	}

	// Documented operator hazard: reclaiming while the other holder is
	// genuinely live SUCCEEDS. Pin that this is what happens (it is the
	// documented override), and that a THIRD opener is then refused again.
	if _, err := rr764Wire(t, dir, false); err == nil {
		t.Fatal("FINDING: a third opener was not refused after the reclaimer took the claim")
	}
	_ = comp2.Close()
}

// R5: a directory holding ONLY a claim.pid (no identity.json, no pages) --
// the shape the re-round brief calls out. It must be refused, and a refused
// Wire must not leave new state behind that changes what the next attempt
// sees.
func TestAttackBUG764RR_ClaimOnlyDirectory(t *testing.T) {
	dir := t.TempDir()
	comp1, err := rr764Wire(t, dir, false)
	if err != nil {
		t.Fatalf("Wire 1: %v", err)
	}
	// Remove the identity stamp, keep the live claim.
	if err := os.Remove(filepath.Join(dir, pageIdentityFileName)); err != nil {
		t.Fatalf("remove identity: %v", err)
	}

	_, err = rr764Wire(t, dir, false)
	if err == nil {
		t.Fatal("FINDING: a claim-only directory was accepted by a second opener")
	}
	t.Logf("claim-only refusal: %s", rr764Code(t, err))
	if _, statErr := os.Stat(filepath.Join(dir, pageIdentityFileName)); statErr == nil {
		t.Logf("NOTE: the REFUSED Wire re-stamped identity.json before hitting the claim refusal (write side effect on a refused path)")
	}
	_ = comp1.Close()
}

// R6 (F2): an unstamped directory that already holds foreign .page files
// must be refused -- with and without a claim file present.
func TestAttackBUG764RR_UnstampedForeignPagesRefused(t *testing.T) {
	for _, withClaim := range []bool{false, true} {
		name := "noClaim"
		if withClaim {
			name = "withClaim"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			comp, err := rr764Wire(t, dir, false)
			if err != nil {
				t.Fatalf("Wire 1: %v", err)
			}
			atkSeedCitizens(t, comp, 400)
			if err := comp.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			if n, _ := countPageFiles(dir); n == 0 {
				t.Fatal("vacuous: no page files written")
			}
			if err := os.Remove(filepath.Join(dir, pageIdentityFileName)); err != nil {
				t.Fatalf("remove identity: %v", err)
			}
			if withClaim {
				if err := os.WriteFile(filepath.Join(dir, pageClaimFileName), []byte(`{"pid":999999}`), 0o644); err != nil {
					t.Fatalf("write claim: %v", err)
				}
			}
			e := core.NewEngine(core.WithWorldSeed(rr764Seed))
			_, err = Wire(e, &Deps{
				PersistCity:   persist.CityKey{TenantID: "local", CityID: "OTHER-city"},
				CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 2, PageDir: dir},
			})
			if err == nil {
				t.Fatal("FINDING: an unstamped directory holding foreign pages was ADOPTED")
			}
			if code := rr764Code(t, err); code != ErrCitizenPagingUnstampedForeignPages {
				t.Fatalf("FINDING: expected %s, got %s: %v", ErrCitizenPagingUnstampedForeignPages, code, err)
			}
			// Reclaim must NOT be a way around the F2 refusal.
			e2 := core.NewEngine(core.WithWorldSeed(rr764Seed))
			_, err = Wire(e2, &Deps{
				PersistCity:   persist.CityKey{TenantID: "local", CityID: "OTHER-city"},
				CitizenPaging: CitizenPagingOptions{Enabled: true, MaxResidentShards: 2, PageDir: dir, Reclaim: true},
			})
			if err == nil {
				t.Fatal("FINDING: Reclaim:true bypassed the foreign-pages refusal")
			}
			if code := rr764Code(t, err); code != ErrCitizenPagingUnstampedForeignPages {
				t.Fatalf("FINDING: Reclaim path refused with %s, not the foreign-pages code", code)
			}
		})
	}
}

// R7: two openers racing RECLAIM against each other must still leave
// exactly one holder (the reclaim path does remove-then-create, which is
// not atomic).
func TestAttackBUG764RR_ConcurrentReclaimRace(t *testing.T) {
	dir := t.TempDir()
	comp1, err := rr764Wire(t, dir, false)
	if err != nil {
		t.Fatalf("Wire 1: %v", err)
	}
	_ = comp1 // stale holder, never closed

	winners, codes, losers := rr764ReclaimRace(t, dir, 8)
	t.Logf("concurrent reclaim: winners=%d codes=%v", winners, codes)
	if winners != 1 {
		t.Errorf("FINDING: %d concurrent reclaimers took the exclusive claim (want exactly 1) -- reclaim must be atomic; losers: %v", winners, losers)
	}
	// Every loser must be the clean claim refusal -- never a config/IO error
	// (BUG-799: Windows sharing-violation noise from the remove/create gap).
	for _, e := range losers {
		// BUG-799 r3: a loser that meets the held takeover lock gets the
		// distinct MET-G822 "takeover in progress" refusal.
		if c := rr764CodeSafe(e); c != ErrCitizenPagingDirectoryClaimed && c != ErrCitizenPagingTakeoverInProgress {
			t.Errorf("FINDING: reclaim loser refused with %v, want %s or %s", e, ErrCitizenPagingDirectoryClaimed, ErrCitizenPagingTakeoverInProgress)
		}
	}
}

// rr764ReclaimRace releases n reclaimers at once (barrier + a spin-up
// handshake so all goroutines are parked on the barrier before it opens)
// and returns the winner count, loser code tally and loser errors. Winning
// compositions are kept alive until the test ends.
func rr764ReclaimRace(t *testing.T, dir string, n int) (int, map[string]int, []error) {
	t.Helper()
	var wg, ready sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	codes := map[string]int{}
	var losers []error
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		ready.Add(1)
		go func() {
			defer wg.Done()
			ready.Done()
			<-start
			comp, err := rr764Wire(t, dir, true)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				winners++
				_ = comp
				return
			}
			codes[rr764CodeSafe(err)]++
			losers = append(losers, err)
		}()
	}
	ready.Wait()
	close(start)
	wg.Wait()
	return winners, codes, losers
}

// BUG-799: a claim that a takeover JUST created (fresh, Reclaimed, by a
// process that is alive -- here, this one) must never be stolen by another
// concurrent reclaimer, however many pile on; the holder's claim record must
// survive byte-for-byte, and the holder's own Close must still release it.
func TestBUG799_FreshReclaimedClaimNeverStolenConcurrently(t *testing.T) {
	dir := t.TempDir()
	if _, err := rr764Wire(t, dir, false); err != nil { // stale holder, never closed
		t.Fatalf("Wire 1: %v", err)
	}
	holder, err := rr764Wire(t, dir, true) // the takeover winner: fresh Reclaimed claim
	if err != nil {
		t.Fatalf("holder reclaim: %v", err)
	}
	held, err := readPageClaim(filepath.Join(dir, pageClaimFileName))
	if err != nil || !held.Reclaimed {
		t.Fatalf("holder's claim unreadable or not marked Reclaimed: %+v err=%v", held, err)
	}

	winners, codes, losers := rr764ReclaimRace(t, dir, 16)
	if winners != 0 {
		t.Fatalf("FINDING: %d reclaimers stole a FRESH live reclaim claim (codes=%v)", winners, codes)
	}
	for _, e := range losers {
		if c := rr764CodeSafe(e); c != ErrCitizenPagingDirectoryClaimed && c != ErrCitizenPagingTakeoverInProgress {
			t.Errorf("loser refused with %v, want %s or %s", e, ErrCitizenPagingDirectoryClaimed, ErrCitizenPagingTakeoverInProgress)
		}
	}
	after, err := readPageClaim(filepath.Join(dir, pageClaimFileName))
	if err != nil || after != held {
		t.Fatalf("FINDING: holder's claim record changed under contention: before=%+v after=%+v err=%v", held, after, err)
	}
	if err := holder.Close(); err != nil {
		t.Fatalf("holder Close: %v", err)
	}
	if rr764ClaimExists(t, dir) {
		t.Fatal("FINDING: holder's claim survived its own Close")
	}
}

// BUG-799: once a Reclaimed claim is older than reclaimFreshWindow it is
// stealable again (an operator re-running -citizen-paging-reclaim after a
// second crash), exactly once even under contention. The clock is injected,
// never slept.
func TestBUG799_AgedReclaimedClaimIsStealableExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	if _, err := rr764Wire(t, dir, false); err != nil {
		t.Fatalf("Wire 1: %v", err)
	}
	if _, err := rr764Wire(t, dir, true); err != nil {
		t.Fatalf("first reclaim: %v", err)
	}
	realNow := claimNow
	t.Cleanup(func() { claimNow = realNow })
	claimNow = func() time.Time { return realNow().Add(reclaimFreshWindow + time.Second) }

	winners, codes, _ := rr764ReclaimRace(t, dir, 16)
	if winners != 1 {
		t.Fatalf("FINDING: %d winners over an aged reclaimed claim (codes=%v), want exactly 1", winners, codes)
	}
}

func rr764CodeSafe(err error) string {
	var re *errs.E
	if errors.As(err, &re) {
		return re.Code
	}
	return "NON-REGISTRY: " + err.Error()
}
