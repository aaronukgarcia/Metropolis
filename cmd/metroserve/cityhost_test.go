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
	"unsafe"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079936 Phase 2 inc1 — CityHost registry tests.
//
// The correctness bar is: (AC-4) two cities in one host never share engine
// state or journal; (AC-2/6) concurrent GetOrCreate of the same key yields
// exactly one engine and -race clean; (AC-5) a city rehydrates byte-identically
// across a host restart; and no goroutine leaks on Shutdown/Close. The tests
// reuse inc4's rtCommands/submitAll/newEngine helpers (persist_test.go).
//
// Every host here uses a tickInterval of hostTickDisabled (an hour) so the
// per-city wall-clock tick driver never fires during a test — the engine
// advances ONLY by the commands the test submits, exactly as inc4's
// no-goroutine setUpPersistence tests do, keeping digests deterministic. Pool
// size is pinned to 1 (testEngineOpts) to match inc4's fast/deterministic
// engine; the standalone control engines a test compares against use the same
// seed (seedForCity) and pool size, so a digest match is meaningful.

const hostTickDisabled = time.Hour

func testEngineOpts() []core.Option { return []core.Option{core.WithPoolSize(1)} }

// newTestHost builds a host wired for deterministic, quiet, tick-free tests and
// registers Close() as cleanup so no test leaks a city's goroutines.
func newTestHost(t *testing.T, persistDir string) *CityHost {
	t.Helper()
	h, err := NewCityHost(persistDir, hostTickDisabled)
	if err != nil {
		t.Fatalf("NewCityHost(%q): %v", persistDir, err)
	}
	h.engineOpts = testEngineOpts()
	h.logw = io.Discard
	t.Cleanup(func() { _ = h.Close() })
	return h
}

// standaloneDigest builds a single, standalone engine at the SAME per-city seed
// the host would derive, feeds it cmds, and returns its StateDigest — the
// independent control every isolation/durability assertion compares against.
func standaloneDigest(t *testing.T, key persist.CityKey, cmds []protocol.Command) [32]byte {
	t.Helper()
	opts := append([]core.Option{core.WithWorldSeed(seedForCity(key))}, testEngineOpts()...)
	e := core.NewEngine(opts...)
	comp, err := compose.Wire(e, nil)
	if err != nil {
		t.Fatalf("standalone Wire: %v", err)
	}
	submitAll(t, e, cmds)
	return comp.StateDigest()
}

// cityCount reads the number of registered cities under the host lock.
func cityCount(h *CityHost) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.cities)
}

// hasCity reports whether a key is registered, under the host lock.
func hasCity(h *CityHost, key persist.CityKey) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	_, ok := h.cities[key]
	return ok
}

// divergentSeq is rtCommands plus one extra tick, so a second city fed it
// reaches a genuinely different StateDigest from a city fed rtCommands.
func divergentSeq() []protocol.Command {
	return append(rtCommands(), protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID("divergent-extra-adv"),
		Kind:            protocol.KindAdvanceTicks,
		Payload:         protocol.AdvanceTicksPayload{N: 1},
	})
}

// TestCityHost_Isolation (AC-4): two cities in one host, driven with divergent
// command sequences, each reach the SAME digest a standalone single-city engine
// at the same seed reaches — proving neither leaks into the other and each is a
// fully independent engine.
func TestCityHost_Isolation(t *testing.T) {
	h := newTestHost(t, "") // no-persist mode
	ctx := context.Background()

	keyA := persist.CityKey{TenantID: "local", CityID: "alpha"}
	keyB := persist.CityKey{TenantID: "local", CityID: "beta"}

	rcA, err := h.GetOrCreate(ctx, keyA)
	if err != nil {
		t.Fatalf("GetOrCreate(A): %v", err)
	}
	rcB, err := h.GetOrCreate(ctx, keyB)
	if err != nil {
		t.Fatalf("GetOrCreate(B): %v", err)
	}
	if rcA == rcB {
		t.Fatal("two distinct keys returned the SAME runningCity — cities are not isolated")
	}

	seqA := rtCommands()
	seqB := divergentSeq()
	submitAll(t, rcA.Engine(), seqA)
	submitAll(t, rcB.Engine(), seqB)

	digestA := rcA.Composition().StateDigest()
	digestB := rcB.Composition().StateDigest()

	if digestA == digestB {
		t.Fatal("test invalid: the two cities produced identical digests, so isolation cannot be observed")
	}
	// Each host city matches its OWN standalone control, and NOT the other's —
	// the multi-tenant isolation guarantee.
	if want := standaloneDigest(t, keyA, seqA); digestA != want {
		t.Fatalf("city A digest %x != standalone %x (state leaked or wrong seed)", digestA, want)
	}
	if want := standaloneDigest(t, keyB, seqB); digestB != want {
		t.Fatalf("city B digest %x != standalone %x (state leaked or wrong seed)", digestB, want)
	}
	// Cross-check: A must NOT equal B's standalone (belt-and-braces no-leak).
	if crossB := standaloneDigest(t, keyB, seqB); digestA == crossB {
		t.Fatal("city A matched city B's standalone digest — cross-contamination")
	}
}

// TestCityHost_RestartDurability (AC-5): a city persisted by one host rehydrates
// byte-identically into a NEW host on the same dir. Prove-can-fail: a
// never-persisted key on the second host has a different digest.
func TestCityHost_RestartDurability(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	keyA := persist.CityKey{TenantID: "local", CityID: "rt"}

	// Host 1: create A, drive it, snapshot the digest, then Close.
	h1, err := NewCityHost(dir, hostTickDisabled)
	if err != nil {
		t.Fatalf("NewCityHost(1): %v", err)
	}
	h1.engineOpts = testEngineOpts()
	h1.logw = io.Discard
	rcA, err := h1.GetOrCreate(ctx, keyA)
	if err != nil {
		t.Fatalf("GetOrCreate(A) on host1: %v", err)
	}
	submitAll(t, rcA.Engine(), rtCommands())
	digestA := rcA.Composition().StateDigest()
	if err := h1.Close(); err != nil {
		t.Fatalf("host1.Close: %v", err)
	}

	// Host 2: same dir, rehydrate A. Digest must match byte-for-byte.
	h2 := newTestHost(t, dir)
	rcA2, err := h2.GetOrCreate(ctx, keyA)
	if err != nil {
		t.Fatalf("GetOrCreate(A) on host2 (rehydrate): %v", err)
	}
	if got := rcA2.Composition().StateDigest(); got != digestA {
		t.Fatalf("restart is lossy: host1 A digest %x != host2 A digest %x", digestA, got)
	}

	// Prove-can-fail: a fresh, never-persisted key must NOT match A's digest,
	// otherwise the equality above is vacuous.
	fresh := persist.CityKey{TenantID: "local", CityID: "never-persisted"}
	rcFresh, err := h2.GetOrCreate(ctx, fresh)
	if err != nil {
		t.Fatalf("GetOrCreate(fresh): %v", err)
	}
	if rcFresh.Composition().StateDigest() == digestA {
		t.Fatal("a never-persisted city matched the rehydrated digest — the round-trip check cannot detect divergence (false-pass)")
	}
}

// TestCityHost_ConcurrentSameKeyYieldsOne (AC-2/6): N goroutines racing to
// GetOrCreate the SAME key all receive the identical *runningCity, and exactly
// one city is registered. Run under -race -count=2 by the CI gate.
func TestCityHost_ConcurrentSameKeyYieldsOne(t *testing.T) {
	h := newTestHost(t, "")
	ctx := context.Background()
	key := persist.CityKey{TenantID: "local", CityID: "hot"}

	const n = 32
	results := make([]*runningCity, n)
	errsOut := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			results[i], errsOut[i] = h.GetOrCreate(ctx, key)
		}(i)
	}
	wg.Wait()

	for i := 0; i < n; i++ {
		if errsOut[i] != nil {
			t.Fatalf("goroutine %d: GetOrCreate error: %v", i, errsOut[i])
		}
		if results[i] == nil {
			t.Fatalf("goroutine %d: nil runningCity", i)
		}
		if results[i] != results[0] {
			t.Fatalf("goroutine %d got a DIFFERENT runningCity (%p) than goroutine 0 (%p) — a second engine was built for one key", i, results[i], results[0])
		}
	}
	if c := cityCount(h); c != 1 {
		t.Fatalf("concurrent same-key GetOrCreate registered %d cities, want exactly 1", c)
	}
}

// TestCityHost_ConcurrentDifferentKeys (AC-6): concurrent GetOrCreate of
// distinct keys all succeed (no deadlock), each a distinct city.
func TestCityHost_ConcurrentDifferentKeys(t *testing.T) {
	h := newTestHost(t, "")
	ctx := context.Background()

	const n = 24
	results := make([]*runningCity, n)
	errsOut := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			key := persist.CityKey{TenantID: "local", CityID: fmt.Sprintf("c%d", i)}
			results[i], errsOut[i] = h.GetOrCreate(ctx, key)
		}(i)
	}
	wg.Wait()

	seen := make(map[*runningCity]bool, n)
	for i := 0; i < n; i++ {
		if errsOut[i] != nil {
			t.Fatalf("goroutine %d: GetOrCreate error: %v", i, errsOut[i])
		}
		if seen[results[i]] {
			t.Fatalf("goroutine %d returned a runningCity already handed to another key", i)
		}
		seen[results[i]] = true
	}
	if c := cityCount(h); c != n {
		t.Fatalf("registered %d cities, want %d", c, n)
	}
}

// TestCityHost_ShutdownStopsOneCity (AC-3): Shutdown removes exactly the named
// city, leaves the rest running, and is idempotent (repeat + unknown key).
func TestCityHost_ShutdownStopsOneCity(t *testing.T) {
	h := newTestHost(t, "")
	ctx := context.Background()
	keyA := persist.CityKey{TenantID: "local", CityID: "A"}
	keyB := persist.CityKey{TenantID: "local", CityID: "B"}

	if _, err := h.GetOrCreate(ctx, keyA); err != nil {
		t.Fatalf("GetOrCreate(A): %v", err)
	}
	rcB, err := h.GetOrCreate(ctx, keyB)
	if err != nil {
		t.Fatalf("GetOrCreate(B): %v", err)
	}

	if err := h.Shutdown(keyA); err != nil {
		t.Fatalf("Shutdown(A): %v", err)
	}
	if hasCity(h, keyA) {
		t.Fatal("Shutdown(A) left A registered")
	}
	if !hasCity(h, keyB) {
		t.Fatal("Shutdown(A) wrongly removed B")
	}
	// Idempotent: repeat and unknown-key shutdowns are no-op nil.
	if err := h.Shutdown(keyA); err != nil {
		t.Fatalf("Shutdown(A) second call not idempotent: %v", err)
	}
	if err := h.Shutdown(persist.CityKey{TenantID: "local", CityID: "ghost"}); err != nil {
		t.Fatalf("Shutdown(unknown) not a no-op: %v", err)
	}

	// B is still fully live: it still accepts and processes commands.
	submitAll(t, rcB.Engine(), rtCommands())
	if rcB.Composition().StateDigest() == ([32]byte{}) {
		t.Fatal("city B produced a zero digest after A's shutdown (B may have been torn down too)")
	}
}

// TestCityHost_CloseNoGoroutineLeak (AC-3): creating cities then Close()ing the
// host returns the goroutine count to its pre-host baseline — every per-city
// pump/command-loop/tick goroutine is joined, none leaked.
func TestCityHost_CloseNoGoroutineLeak(t *testing.T) {
	// Settle any goroutines a prior subtest left mid-exit, then baseline.
	settleGoroutines()
	base := runtime.NumGoroutine()

	h, err := NewCityHost("", hostTickDisabled)
	if err != nil {
		t.Fatalf("NewCityHost: %v", err)
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
	// Each city runs 3 goroutines (pump, command loop, tick), so the count must
	// have risen well above baseline while they run.
	if live := runtime.NumGoroutine(); live <= base {
		t.Fatalf("expected goroutine count to rise above baseline %d with %d live cities, got %d", base, n, live)
	}

	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Close joins every city's goroutines before returning, so the count must
	// drop back to (approximately) the baseline. Poll briefly to absorb the
	// runtime's own scheduler bookkeeping.
	if !waitForGoroutines(base+1, 2*time.Second) {
		t.Fatalf("goroutines did not drain after Close: baseline %d, still %d — a per-city goroutine leaked", base, runtime.NumGoroutine())
	}
}

// TestCityHost_CorruptJournalFatalPerCity (non-negotiable): a corrupt journal
// makes THAT city's GetOrCreate fatal (error, no half-built registration) while
// other cities under the same host are unaffected.
func TestCityHost_CorruptJournalFatalPerCity(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// Plant a garbage journal frame for the "corrupt" city on disk.
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	corruptKey := persist.CityKey{TenantID: "local", CityID: "corrupt"}
	if err := disk.AppendJournal(ctx, corruptKey, []byte("{not a valid command frame")); err != nil {
		t.Fatalf("plant garbage: %v", err)
	}

	h := newTestHost(t, dir)

	// A healthy city on the same host builds fine.
	goodKey := persist.CityKey{TenantID: "local", CityID: "good"}
	if _, err := h.GetOrCreate(ctx, goodKey); err != nil {
		t.Fatalf("GetOrCreate(good): %v", err)
	}

	// The corrupt city's GetOrCreate is FATAL and registers nothing.
	if _, err := h.GetOrCreate(ctx, corruptKey); err == nil {
		t.Fatal("corrupt journal was NOT fatal — GetOrCreate silently started fresh over a persisted city")
	}
	if hasCity(h, corruptKey) {
		t.Fatal("a half-built corrupt city was left registered in the map")
	}
	// The good city is untouched and still registered.
	if !hasCity(h, goodKey) {
		t.Fatal("the corrupt-city failure removed the healthy city")
	}
}

// hostByteCopy performs SEC-020's attack — a raw byte-for-byte copy of a
// CityHost — mirroring persist's diskByteCopy. A literal `cp := *h` is
// forbidden: go vet's copylocks flags copying a struct containing a
// sync.Mutex / atomic.Pointer, and CI's go vet is a gate.
func hostByteCopy(h *CityHost) *CityHost {
	cp := new(CityHost)
	*(*[unsafe.Sizeof(CityHost{})]byte)(unsafe.Pointer(cp)) = *(*[unsafe.Sizeof(CityHost{})]byte)(unsafe.Pointer(h))
	return cp
}

// TestCityHost_CopyRejected (SEC-020): a value copy of a CityHost — whose self
// pointer no longer equals its own address — is rejected by every mutating
// method BEFORE any lock is taken, never a deadlock or aliased-map mutation.
func TestCityHost_CopyRejected(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer close(done)

		h, err := NewCityHost("", hostTickDisabled)
		if err != nil {
			t.Errorf("NewCityHost: %v", err)
			return
		}
		defer func() { _ = h.Close() }()

		cp := hostByteCopy(h)
		ctx := context.Background()
		key := persist.CityKey{TenantID: "local", CityID: "c"}

		if _, err := cp.GetOrCreate(ctx, key); !errors.Is(err, errCityHostCopied) {
			t.Errorf("copied GetOrCreate = %v, want errCityHostCopied", err)
		}
		if err := cp.Shutdown(key); !errors.Is(err, errCityHostCopied) {
			t.Errorf("copied Shutdown = %v, want errCityHostCopied", err)
		}
		if err := cp.Close(); !errors.Is(err, errCityHostCopied) {
			t.Errorf("copied Close = %v, want errCityHostCopied", err)
		}
		// A bare zero-value host (self never Stored) is rejected identically.
		var bare CityHost
		if _, err := bare.GetOrCreate(ctx, key); !errors.Is(err, errCityHostCopied) {
			t.Errorf("bare GetOrCreate = %v, want errCityHostCopied", err)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("TestCityHost_CopyRejected hung — a guard was not the first statement before a Lock()")
	}
}

// settleGoroutines yields a few times so goroutines from a just-finished subtest
// have a chance to exit before a baseline is taken.
func settleGoroutines() {
	for i := 0; i < 100; i++ {
		runtime.Gosched()
	}
	time.Sleep(20 * time.Millisecond)
}

// waitForGoroutines polls until the live goroutine count is <= target or the
// timeout elapses, returning whether it drained in time.
func waitForGoroutines(target int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= target {
			return true
		}
		runtime.Gosched()
		time.Sleep(5 * time.Millisecond)
	}
	return runtime.NumGoroutine() <= target
}
