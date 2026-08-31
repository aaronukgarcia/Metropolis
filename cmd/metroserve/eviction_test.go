package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// eviction_test.go — FEAT-1972079942 (CityHost idle-city eviction) tests.
//
// Correctness bar: (AC-2) refcount never underflows and a stray Release/Acquire
// is a benign no-op; (AC-3) an idle city is unloaded after the timeout and the
// evictor joins its goroutines; (AC-4) eviction never races a reconnect — no
// torn/lost city, -race clean, and a rehydrated-after-evict city is byte-
// identical; (AC-5) no goroutine leak; SEC-020 the new Acquire/Release honour
// the copy guard. Every host uses hostTickDisabled so the per-city tick driver
// never fires, keeping state deterministic (reused from cityhost_test.go).

// newEvictHost builds a host with tiny eviction tunables so idle eviction fires
// in milliseconds, wired quiet + deterministic + tick-free, with Close on
// cleanup. The tunables are fixed at construction (before the evictor goroutine
// starts), so they are seen race-free by that goroutine.
func newEvictHost(t *testing.T, persistDir string, idleTimeout, sweep time.Duration) *CityHost {
	t.Helper()
	h, err := newCityHost(persistDir, hostTickDisabled, idleTimeout, sweep)
	if err != nil {
		t.Fatalf("newCityHost(%q): %v", persistDir, err)
	}
	h.engineOpts = testEngineOpts()
	h.logw = io.Discard
	t.Cleanup(func() { _ = h.Close() })
	return h
}

// activeCount reads a city's active-connection refcount under the host lock, or
// -1 if the city is not registered.
func activeCount(h *CityHost, key persist.CityKey) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.cities[key]
	if !ok {
		return -1
	}
	return e.active
}

// waitForNoCity polls until key is no longer registered (evicted) or the timeout
// elapses, returning whether it was evicted.
func waitForNoCity(h *CityHost, key persist.CityKey, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !hasCity(h, key) {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return !hasCity(h, key)
}

// TestEviction_RefcountNoUnderflow (AC-2): Acquire/Release track a per-city
// count that never goes negative; a stray Release (no matching Acquire, or an
// unknown city) and an Acquire on an unknown city are benign no-ops, never a
// panic.
func TestEviction_RefcountNoUnderflow(t *testing.T) {
	h := newEvictHost(t, "", time.Hour, time.Hour) // eviction effectively off
	ctx := context.Background()
	key := persist.CityKey{TenantID: "local", CityID: "counted"}

	if _, err := h.GetOrCreate(ctx, key); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if got := activeCount(h, key); got != 0 {
		t.Fatalf("fresh city active = %d, want 0", got)
	}

	h.Acquire(key.TenantID, key.CityID)
	h.Acquire(key.TenantID, key.CityID)
	if got := activeCount(h, key); got != 2 {
		t.Fatalf("after 2 Acquire, active = %d, want 2", got)
	}
	h.Release(key.TenantID, key.CityID)
	h.Release(key.TenantID, key.CityID)
	if got := activeCount(h, key); got != 0 {
		t.Fatalf("after 2 Release, active = %d, want 0", got)
	}

	// Stray Release (count already 0) must NOT drive it negative or panic.
	h.Release(key.TenantID, key.CityID)
	if got := activeCount(h, key); got != 0 {
		t.Fatalf("stray Release drove active to %d, want it to stay 0", got)
	}

	// Acquire/Release on an entirely unknown city are no-ops, never a panic.
	h.Acquire("local", "ghost")
	h.Release("local", "ghost")
	if got := activeCount(h, persist.CityKey{TenantID: "local", CityID: "ghost"}); got != -1 {
		t.Fatalf("unknown-city Acquire/Release created a registration (active=%d)", got)
	}
}

// TestEviction_IdleFires (AC-3): a city with zero connections for longer than
// the idle timeout is evicted by the background sweep. Prove-can-fail: a city
// held by an Acquire is NEVER evicted under the same wait.
func TestEviction_IdleFires(t *testing.T) {
	const idle = 30 * time.Millisecond
	h := newEvictHost(t, "", idle, 5*time.Millisecond)
	ctx := context.Background()

	idleKey := persist.CityKey{TenantID: "local", CityID: "idle"}
	pinnedKey := persist.CityKey{TenantID: "local", CityID: "pinned"}

	if _, err := h.GetOrCreate(ctx, idleKey); err != nil {
		t.Fatalf("GetOrCreate(idle): %v", err)
	}
	if _, err := h.GetOrCreate(ctx, pinnedKey); err != nil {
		t.Fatalf("GetOrCreate(pinned): %v", err)
	}
	// Pin the second city with a live connection so it must survive.
	h.Acquire(pinnedKey.TenantID, pinnedKey.CityID)

	// The idle city must be evicted well within a generous multiple of the idle
	// timeout; the pinned city must remain the whole time.
	if !waitForNoCity(h, idleKey, 2*time.Second) {
		t.Fatal("idle city was not evicted after the idle timeout elapsed")
	}
	if !hasCity(h, pinnedKey) {
		t.Fatal("a city with an active connection was wrongly evicted")
	}

	// Prove-can-fail sanity: once released, the pinned city also becomes idle and
	// is eventually evicted — so the survival above was due to the pin, not a
	// broken evictor.
	h.Release(pinnedKey.TenantID, pinnedKey.CityID)
	if !waitForNoCity(h, pinnedKey, 2*time.Second) {
		t.Fatal("released city was never evicted — the survival check would be vacuous")
	}
}

// TestEviction_RehydrateAfterEvictDigest (AC-4): a persisted city that is driven,
// evicted while idle, and then reconnected rehydrates byte-identically from its
// journal — its post-rehydrate digest equals both its pre-eviction digest and a
// standalone control at the same seed+commands.
func TestEviction_RehydrateAfterEvictDigest(t *testing.T) {
	dir := t.TempDir()
	const idle = 25 * time.Millisecond
	h := newEvictHost(t, dir, idle, 5*time.Millisecond)
	ctx := context.Background()
	key := persist.CityKey{TenantID: persistTenantID, CityID: "rehydrate"}

	// Build + pin (so setup is not evicted mid-flight), drive, snapshot, release.
	rc, err := h.GetOrCreate(ctx, key)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	h.Acquire(key.TenantID, key.CityID)
	submitAll(t, rc.Engine(), rtCommands())
	preDigest := rc.Composition().StateDigest()
	h.Release(key.TenantID, key.CityID)

	// Let the idle evictor unload it.
	if !waitForNoCity(h, key, 2*time.Second) {
		t.Fatal("idle persisted city was not evicted")
	}

	// Reconnect: GetOrCreate rebuilds by replaying the journal. Digest must match
	// the pre-eviction digest byte-for-byte.
	rc2, err := h.GetOrCreate(ctx, key)
	if err != nil {
		t.Fatalf("GetOrCreate after evict (rehydrate): %v", err)
	}
	if rc2 == rc {
		t.Fatal("rehydrated city is the SAME *runningCity as the evicted one — it was never actually torn down")
	}
	postDigest := rc2.Composition().StateDigest()
	if postDigest != preDigest {
		t.Fatalf("rehydrate-after-evict is lossy: pre %x != post %x", preDigest, postDigest)
	}
	if want := standaloneDigest(t, key, rtCommands()); postDigest != want {
		t.Fatalf("rehydrated digest %x != standalone control %x", postDigest, want)
	}
}

// TestEviction_ReconnectRaceStress (AC-4, the P0): many goroutines concurrently
// bind (GetOrCreate) → pin (Acquire) → read → release their own city, while an
// aggressive evictor sweeps, with deliberate idle gaps forcing real evict+rebuild
// cycles. Invariants under `go test -race -count=3`: GetOrCreate never errors and
// never returns a torn city (a city read while pinned always has the genesis
// digest — its journal is empty, so every fresh build AND every rehydrate lands
// there), and no refcount is left non-zero.
func TestEviction_ReconnectRaceStress(t *testing.T) {
	dir := t.TempDir()
	// A timeout comfortably larger than the adjacent GetOrCreate→Acquire gap (so
	// a pinned read is never torn) but small enough that the explicit idle sleep
	// below forces genuine evictions between iterations.
	const idle = 40 * time.Millisecond
	h := newEvictHost(t, dir, idle, 3*time.Millisecond)
	ctx := context.Background()

	const workers = 12
	const iters = 6
	// Pre-compute each worker's genesis control digest (empty journal → genesis).
	wants := make([][32]byte, workers)
	keys := make([]persist.CityKey, workers)
	for w := 0; w < workers; w++ {
		keys[w] = persist.CityKey{TenantID: persistTenantID, CityID: fmt.Sprintf("w%d", w)}
		wants[w] = standaloneDigest(t, keys[w], nil)
	}

	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer wg.Done()
			key := keys[w]
			for k := 0; k < iters; k++ {
				rc, err := h.GetOrCreate(ctx, key) // bind
				if err != nil {
					t.Errorf("worker %d iter %d: GetOrCreate: %v", w, k, err)
					return
				}
				h.Acquire(key.TenantID, key.CityID) // pin: now evict-proof
				if rc == nil || rc.Composition() == nil {
					t.Errorf("worker %d iter %d: torn city (nil handle)", w, k)
					h.Release(key.TenantID, key.CityID)
					return
				}
				// Read while pinned — a torn/wrongly-rehydrated city would differ.
				if got := rc.Composition().StateDigest(); got != wants[w] {
					t.Errorf("worker %d iter %d: digest %x != genesis %x (torn or lossy rehydrate)", w, k, got, wants[w])
				}
				h.Release(key.TenantID, key.CityID)
				// Sit idle long enough to be evicted before the next bind, forcing
				// an evict+rebuild interleave (unpinned, so always safe).
				time.Sleep(2 * idle)
			}
		}(w)
	}
	wg.Wait()

	// No refcount left dangling: every still-registered city is back to 0.
	h.mu.Lock()
	for key, e := range h.cities {
		if e.active != 0 {
			t.Errorf("city %s left with active=%d after all workers released", key.CityID, e.active)
		}
	}
	h.mu.Unlock()
}

// TestEviction_NoGoroutineLeak (AC-5): creating cities, letting them all evict,
// then Close returns the goroutine count to baseline — eviction joins each
// city's 3 goroutines and Close joins the evictor.
func TestEviction_NoGoroutineLeak(t *testing.T) {
	settleGoroutines()
	base := runtime.NumGoroutine()

	const idle = 20 * time.Millisecond
	h, err := newCityHost("", hostTickDisabled, idle, 3*time.Millisecond)
	if err != nil {
		t.Fatalf("newCityHost: %v", err)
	}
	h.engineOpts = testEngineOpts()
	h.logw = io.Discard

	ctx := context.Background()
	const n = 8
	for i := 0; i < n; i++ {
		if _, err := h.GetOrCreate(ctx, persist.CityKey{TenantID: "local", CityID: fmt.Sprintf("c%d", i)}); err != nil {
			t.Fatalf("GetOrCreate(c%d): %v", i, err)
		}
	}
	if live := runtime.NumGoroutine(); live <= base {
		t.Fatalf("expected goroutine count to rise above baseline %d with %d live cities, got %d", base, n, live)
	}

	// Let the evictor unload every (idle) city, joining their goroutines.
	deadline := time.Now().Add(2 * time.Second)
	for cityCount(h) > 0 && time.Now().Before(deadline) {
		time.Sleep(3 * time.Millisecond)
	}
	if c := cityCount(h); c != 0 {
		t.Fatalf("expected all %d cities evicted, %d remain", n, c)
	}

	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if !waitForGoroutines(base+1, 2*time.Second) {
		t.Fatalf("goroutines did not drain after evict+Close: baseline %d, still %d", base, runtime.NumGoroutine())
	}
}

// TestEviction_AcquireReleaseCopyGuard (SEC-020): the new mutating methods
// Acquire and Release honour the copy guard — called on a byte-copy of a
// CityHost (whose self pointer no longer matches its address) they are inert
// no-ops (guard tripped before the lock), so they neither deadlock on the
// copied mutex nor mutate the aliased map. Proven by observing the ORIGINAL's
// refcount is untouched by the copy's calls.
func TestEviction_AcquireReleaseCopyGuard(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)

		h := newEvictHost(t, "", time.Hour, time.Hour)
		ctx := context.Background()
		key := persist.CityKey{TenantID: "local", CityID: "guarded"}
		if _, err := h.GetOrCreate(ctx, key); err != nil {
			t.Errorf("GetOrCreate: %v", err)
			return
		}

		cp := hostByteCopy(h) // aliases h's cities map; self no longer matches
		cp.Acquire(key.TenantID, key.CityID)
		cp.Release(key.TenantID, key.CityID)

		// The guard tripped: the copy touched nothing, so the original's count
		// is still 0. (An unguarded Acquire on the copy would have bumped the
		// shared entry to 1.)
		if got := activeCount(h, key); got != 0 {
			t.Errorf("copied Acquire/Release mutated the shared refcount (active=%d, want 0) — the copy guard is not live", got)
		}

		// A bare zero-value host must also no-op without panic.
		var bare CityHost
		bare.Acquire("local", "x")
		bare.Release("local", "x")
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("copy-guard test hung — a guard was not the first statement before a Lock()")
	}
}

// idleSinceOf reads a city's idleSince under the host lock, plus whether the key
// is still registered. Used to assert the AC-4 construction invariant directly.
func idleSinceOf(h *CityHost, key persist.CityKey) (time.Time, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e, ok := h.cities[key]
	if !ok {
		return time.Time{}, false
	}
	return e.idleSince, true
}

// touchProbeCmd is a minimal valid command used to probe whether a returned
// city's transport is live: SendCommand returns protocol.ErrTransportClosed on a
// torn (already-stopped) city, and nil (or a benign queue-full) on a live one.
func touchProbeCmd() protocol.Command {
	return protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("touch-during-build-probe"),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 1},
	}
}

// TestAttack_TouchDuringBuild (FEAT-1972079942 AC-4, destructive-round reproducer):
// a SECOND concurrent GetOrCreate for a key whose FIRST claimant is still
// building must NOT stamp a non-zero idleSince on the still-building entry. The
// pre-fix existing-entry "touch" fired on any active==0 entry — which is ALSO
// true during construction — so the concurrent caller stamped idleSince on a
// still-building city; a build outliving the (tiny) idleTimeout was then evicted
// and stop()ed mid-build, handing both callers a TORN city (closed transport) and
// leaving a later GetOrCreate to build a SECOND engine for the same key. This
// falsifies the documented invariant "idleSince stays zero throughout
// construction".
//
// The fix ready-guards the touch (only a city whose `ready` channel is closed may
// be touched). This test FAILS on the pre-fix code (still-building entry evicted
// mid-build) and passes after. It uses the onBuildStart seam to hold each build
// open while the concurrent touch and the aggressive evictor run, with a tiny
// idleTimeout so a still-building stamped entry is torn out within a few sweeps.
func TestAttack_TouchDuringBuild(t *testing.T) {
	const idle = 3 * time.Millisecond
	const sweep = 1 * time.Millisecond
	// Ample margin for the concurrent touch to run and several evictor sweeps to
	// fire against a (pre-fix) stamped still-building entry; the pre-fix path
	// trips the check and returns early, so only the healthy path pays it in full.
	const observeWindow = 15 * time.Millisecond

	h := newEvictHost(t, "", idle, sweep)
	ctx := context.Background()

	// The interleaving is FORCED deterministically via the onBuildStart seam
	// (not probabilistic), so a modest iteration count reliably exercises it —
	// the pre-fix defect trips on iter 0. Kept small so this test stays cheap
	// under -race and never risks Go's default per-binary test timeout.
	const iters = 30
	for i := 0; i < iters; i++ {
		key := persist.CityKey{TenantID: "local", CityID: fmt.Sprintf("tdb-%d", i)}

		started := make(chan struct{})
		proceed := make(chan struct{})
		// FIRST claimant holds the build open here (after the entry is published,
		// before construction) until we release it. Set before launching caller1,
		// so the go-statement happens-before makes the read in GetOrCreate race-free.
		h.onBuildStart = func() {
			close(started)
			<-proceed
		}

		var rc1 *runningCity
		var err1 error
		done1 := make(chan struct{})
		go func() {
			rc1, err1 = h.GetOrCreate(ctx, key) // the claimant: builds (held open)
			close(done1)
		}()

		<-started // entry published, build held open

		var rc2 *runningCity
		var err2 error
		done2 := make(chan struct{})
		go func() {
			rc2, err2 = h.GetOrCreate(ctx, key) // existing-entry branch: the "touch"
			close(done2)
		}()

		// While the build is held open, watch the still-building entry for the
		// AC-4 invariant violation: it must keep idleSince ZERO and must not be
		// evicted. On the pre-fix code the concurrent touch stamps idleSince
		// (caught immediately) and the evictor then deletes the entry mid-build
		// (torn city); on the fixed code the touch is skipped, so it stays zero and
		// present for the whole window. Break early on any violation so the buggy
		// path is fast; only the healthy path pays the full window.
		var violation string
		for deadline := time.Now().Add(observeWindow); time.Now().Before(deadline); {
			since, present := idleSinceOf(h, key)
			if !present {
				violation = "still-building entry was evicted mid-build (torn city)"
				break
			}
			if !since.IsZero() {
				violation = fmt.Sprintf("still-building entry has non-zero idleSince %v (touch stamped a building city)", since)
				break
			}
			time.Sleep(time.Millisecond)
		}

		close(proceed) // let the build finish
		<-done1
		<-done2

		if violation != "" {
			t.Fatalf("iter %d: city %s — %s (AC-4 invariant \"idleSince stays zero throughout construction\" violated)", i, key.CityID, violation)
		}

		if err1 != nil || rc1 == nil {
			t.Fatalf("iter %d: claimant GetOrCreate failed: rc=%v err=%v", i, rc1, err1)
		}
		if err2 != nil || rc2 == nil {
			t.Fatalf("iter %d: concurrent GetOrCreate failed: rc=%v err=%v", i, rc2, err2)
		}
		if rc1 != rc2 {
			t.Fatalf("iter %d: concurrent same-key callers got DIFFERENT cities (%p vs %p) — a second engine was built for one key", i, rc1, rc2)
		}

		// Torn-city transport probe: simulate the onOpen that follows a resolved
		// GetOrCreate by pinning the city, then confirm its transport is LIVE. A
		// city torn out mid-build has a closed transport even though a caller holds
		// it. Only assert when the pin actually took (entry present): a city
		// legitimately idle-evicted after readiness is benign, not this defect.
		h.Acquire(key.TenantID, key.CityID)
		if activeCount(h, key) > 0 {
			if perr := rc1.Transport().SendCommand(touchProbeCmd()); errors.Is(perr, protocol.ErrTransportClosed) {
				t.Fatalf("iter %d: pinned city %s has a CLOSED transport — torn city handed to caller", i, key.CityID)
			}
			h.Release(key.TenantID, key.CityID)
		}
		_ = h.Shutdown(key) // clean up so the next iteration starts fresh
	}
}

// TestAttack_AcquireReleaseDuringBuild (FEAT-1972079942 AC-4, destructive re-round
// reproducer for the SYMMETRIC Release hole): an Acquire+Release pair that
// straddles a still-in-progress construction (active 0→1→0 on a not-yet-ready
// entry) must NOT stamp a non-zero idleSince on the still-building entry. Release
// stamps idleSince when the count returns to 0; without the ready-guard on that
// stamp, a Release that lands mid-build stamps a still-building city, which the
// idle evictor can then delete + stop() before construction finishes — the exact
// torn-city defect the GetOrCreate touch-guard closes, arriving via Release.
//
// This test FAILS with the Release ready-guard removed (still-building entry gets
// a non-zero idleSince and is evicted mid-build) and passes with it. Same
// onBuildStart seam + tiny idleTimeout as TestAttack_TouchDuringBuild; the
// interleaving is forced deterministically (the Acquire/Release run while the
// build is provably held open), so a modest iteration count exercises it.
func TestAttack_AcquireReleaseDuringBuild(t *testing.T) {
	const idle = 3 * time.Millisecond
	const sweep = 1 * time.Millisecond
	const observeWindow = 15 * time.Millisecond

	h := newEvictHost(t, "", idle, sweep)
	ctx := context.Background()

	const iters = 30
	for i := 0; i < iters; i++ {
		key := persist.CityKey{TenantID: "local", CityID: fmt.Sprintf("ardb-%d", i)}

		started := make(chan struct{})
		proceed := make(chan struct{})
		// FIRST claimant holds the build open after the entry is published, before
		// construction. Set before launching the claimant so the go-statement
		// happens-before makes the read in GetOrCreate race-free.
		h.onBuildStart = func() {
			close(started)
			<-proceed
		}

		var rc1 *runningCity
		var err1 error
		done1 := make(chan struct{})
		go func() {
			rc1, err1 = h.GetOrCreate(ctx, key) // the claimant: builds (held open)
			close(done1)
		}()

		<-started // entry published, build held open

		// The symmetric attack: while the build is held open, an Acquire+Release
		// pair straddles construction. Release brings the count back to 0 on a
		// still-building entry and (pre-fix) stamps idleSince, dooming it.
		h.Acquire(key.TenantID, key.CityID)
		h.Release(key.TenantID, key.CityID)

		// Watch the still-building entry: it must keep idleSince ZERO and must not
		// be evicted while the build is held open. Break early on any violation.
		var violation string
		for deadline := time.Now().Add(observeWindow); time.Now().Before(deadline); {
			since, present := idleSinceOf(h, key)
			if !present {
				violation = "still-building entry was evicted mid-build (torn city)"
				break
			}
			if !since.IsZero() {
				violation = fmt.Sprintf("still-building entry has non-zero idleSince %v (Release stamped a building city)", since)
				break
			}
			time.Sleep(time.Millisecond)
		}

		close(proceed) // let the build finish
		<-done1

		if violation != "" {
			t.Fatalf("iter %d: city %s — %s (AC-4 invariant \"idleSince stays zero throughout construction\" violated via Release)", i, key.CityID, violation)
		}
		if err1 != nil || rc1 == nil {
			t.Fatalf("iter %d: claimant GetOrCreate failed: rc=%v err=%v", i, rc1, err1)
		}

		// Torn-city transport probe: a city torn out mid-build has a closed
		// transport even though the caller holds it.
		h.Acquire(key.TenantID, key.CityID)
		if activeCount(h, key) > 0 {
			if perr := rc1.Transport().SendCommand(touchProbeCmd()); errors.Is(perr, protocol.ErrTransportClosed) {
				t.Fatalf("iter %d: pinned city %s has a CLOSED transport — torn city handed to caller", i, key.CityID)
			}
			h.Release(key.TenantID, key.CityID)
		}
		_ = h.Shutdown(key) // clean up so the next iteration starts fresh
	}
}
