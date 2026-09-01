package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/engine/compose"
	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079936 Phase 1 inc3b — INDEPENDENT DESTRUCTIVE ROUND (Opus r1).
//
// These are attacker-authored regressions, not builder tests. They cover the
// matrix snapshotdriver_test.go's single happy-path test does not: the
// cadence off-switch (0 / negative), an exact snapshot count at a cadence
// boundary, a corrupt latest snapshot, cross-city snapshot isolation,
// concurrent transport producers racing the snapshot hook, and the
// BUG-472 journal-swallow divergence.

// ---------------------------------------------------------------------------
// Instrumented stores
// ---------------------------------------------------------------------------

// attackPutCountingStore counts PutSnapshot calls so a test can assert the
// cadence off-switch takes LITERALLY no snapshots, rather than inferring it
// from an empty snapshot list (which a write-then-prune bug would also
// produce).
type attackPutCountingStore struct {
	persist.Store
	puts int64
}

func (s *attackPutCountingStore) PutSnapshot(ctx context.Context, city persist.CityKey, b []byte) (persist.SnapshotID, error) {
	atomic.AddInt64(&s.puts, 1)
	return s.Store.PutSnapshot(ctx, city, b)
}

func (s *attackPutCountingStore) Puts() int64 { return atomic.LoadInt64(&s.puts) }

// attackFailingAppendStore fails the Nth AppendJournal call (1-based) with a
// synthetic error, modelling BUG-472's swallow policy: the journaler logs and
// continues, so the command's state effect is live but its journal frame is
// missing.
type attackFailingAppendStore struct {
	persist.Store
	mu       sync.Mutex
	calls    int64
	failCall int64
}

func (s *attackFailingAppendStore) AppendJournal(ctx context.Context, city persist.CityKey, rec []byte) error {
	s.mu.Lock()
	s.calls++
	n := s.calls
	s.mu.Unlock()
	if n == s.failCall {
		return errors.New("attack: synthetic durable-append failure")
	}
	return s.Store.AppendJournal(ctx, city, rec)
}

// ---------------------------------------------------------------------------
// Shared drivers
// ---------------------------------------------------------------------------

// attackTickCorrelationID is the fixed tick-driver correlation ID these tests
// stamp their AdvanceTicks commands with.
const attackTickCorrelationID = "attack-inc3b-tick"

// attackDriveTicks builds a live city over store, drives n single-tick
// AdvanceTicks commands through the REAL startCommandLoop wiring at the given
// cadence, and returns the final digest and tick. The command loop is stopped
// and joined before returning, so every snapshot write has completed.
func attackDriveTicks(t *testing.T, store persist.Store, city persist.CityKey, cadence, n int64) (digest [32]byte, tick int64) {
	t.Helper()
	e := newEngine()
	comp, err := wireAndRehydrate(context.Background(), e, store, city, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("wireAndRehydrate (live): %v", err)
	}
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	ctx, cancel := context.WithCancel(context.Background())
	loopDone := startCommandLoop(ctx, e, transport, comp, store, city, cadence, attackTickCorrelationID, &bytes.Buffer{})

	for i := int64(1); i <= n; i++ {
		res := sendAndAwait(t, transport, protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(attackTickCorrelationID),
			Kind:            protocol.KindAdvanceTicks,
			Payload:         protocol.AdvanceTicksPayload{N: 1},
		})
		if !res.Accepted {
			t.Fatalf("tick %d rejected: %+v", i, res.Error)
		}
	}
	digest = comp.StateDigest()
	if c, err := e.Clock(); err == nil {
		tick = c.Tick()
	}
	cancel()
	if err := <-loopDone; err != nil {
		t.Fatalf("RunCommandLoop: %v", err)
	}
	_ = transport.Close()
	return digest, tick
}

// ---------------------------------------------------------------------------
// ATTACK 1 — the cadence off-switch must take ZERO snapshots
// ---------------------------------------------------------------------------

// TestAttackInc3b_CadenceOffTakesNoSnapshots drives well past several would-be
// cadence boundaries with snapshotting off (0) and with a NEGATIVE cadence,
// asserting PutSnapshot is never called even once. `--snapshot-every 0` is the
// documented "byte-for-byte pre-inc3b behaviour" escape hatch; a negative
// value is not documented at all, so this pins whichever behaviour ships.
func TestAttackInc3b_CadenceOffTakesNoSnapshots(t *testing.T) {
	for _, cadence := range []int64{0, -5} {
		t.Run(fmt.Sprintf("cadence_%d", cadence), func(t *testing.T) {
			dir := t.TempDir()
			city := persist.CityKey{TenantID: persistTenantID, CityID: "off"}
			disk, err := persist.NewDiskStore(dir)
			if err != nil {
				t.Fatalf("NewDiskStore: %v", err)
			}
			counting := &attackPutCountingStore{Store: disk}

			_, tick := attackDriveTicks(t, counting, city, cadence, 12)
			if tick != 12 {
				t.Fatalf("engine tick %d, want 12 — harness broken", tick)
			}
			if got := counting.Puts(); got != 0 {
				t.Fatalf("cadence %d took %d snapshots, want 0 — the off-switch does not disable snapshotting", cadence, got)
			}
			ids, err := disk.ListSnapshots(context.Background(), city)
			if err != nil {
				t.Fatalf("ListSnapshots: %v", err)
			}
			if len(ids) != 0 {
				t.Fatalf("cadence %d left %d durable snapshots, want 0", cadence, len(ids))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// ATTACK 2 — exact snapshot count at a cadence boundary
// ---------------------------------------------------------------------------

// TestAttackInc3b_ExactSnapshotCountAtCadence drives exactly 3 cadence periods
// plus a remainder and asserts EXACTLY 3 snapshots exist — not ">= 2" as the
// builder's test settles for. An off-by-one in ShouldSnapshotEvery (firing at
// tick%cadence<=1, or including tick 0) changes this count.
func TestAttackInc3b_ExactSnapshotCountAtCadence(t *testing.T) {
	const cadence = int64(5)
	const ticks = int64(3*cadence + 2) // 17 -> boundaries at 5, 10, 15

	dir := t.TempDir()
	city := persist.CityKey{TenantID: persistTenantID, CityID: "exact"}
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	counting := &attackPutCountingStore{Store: disk}

	_, tick := attackDriveTicks(t, counting, city, cadence, ticks)
	if tick != ticks {
		t.Fatalf("engine tick %d, want %d", tick, ticks)
	}
	if got := counting.Puts(); got != 3 {
		t.Fatalf("PutSnapshot called %d times over %d ticks at cadence %d, want exactly 3 (ticks 5,10,15)", got, ticks, cadence)
	}
	ids, err := disk.ListSnapshots(context.Background(), city)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("durable snapshot count = %d, want exactly 3", len(ids))
	}
}

// ---------------------------------------------------------------------------
// ATTACK 3 — a corrupt LATEST snapshot must fail closed
// ---------------------------------------------------------------------------

// attackSnapshotFiles returns the on-disk snapshot .bin paths for a city, in
// the DiskStore's own oldest-first order.
func attackSnapshotFiles(t *testing.T, dir string, city persist.CityKey) []string {
	t.Helper()
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	ids, err := disk.ListSnapshots(context.Background(), city)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	var found []string
	walkErr := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(p, ".bin") && strings.Contains(filepath.ToSlash(p), "/snapshots/") {
			found = append(found, p)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk: %v", walkErr)
	}
	if len(found) != len(ids) {
		t.Fatalf("found %d snapshot files on disk but ListSnapshots reports %d", len(found), len(ids))
	}
	// filepath.Walk is lexical; snapshot ids are zero-padded sequence numbers,
	// so lexical order == oldest-first, matching ListSnapshots.
	return found
}

// TestAttackInc3b_CorruptLatestSnapshotFailsClosed corrupts the newest snapshot
// blob and asserts restore FAILS LOUDLY rather than silently falling back to an
// older snapshot or to a full genesis replay. Either silent fallback would be a
// fail-open: the operator would never learn their newest durable snapshot is
// unreadable. It also asserts the failed restore did not grow the journal.
func TestAttackInc3b_CorruptLatestSnapshotFailsClosed(t *testing.T) {
	const cadence = int64(4)
	dir := t.TempDir()
	cityID := "corrupt"
	city := persist.CityKey{TenantID: persistTenantID, CityID: cityID}
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}

	// Two cadence boundaries -> two snapshots, so an "older snapshot" exists
	// for a fallback to (wrongly) reach for.
	attackDriveTicks(t, disk, city, cadence, 2*cadence+1)
	files := attackSnapshotFiles(t, dir, city)
	if len(files) < 2 {
		t.Fatalf("need >=2 snapshots to test latest-corrupt fallback, got %d", len(files))
	}
	framesBefore := attackJournalFrameCount(t, dir, cityID)

	// Corrupt the NEWEST snapshot in place (keep the file, destroy the zip).
	newest := files[len(files)-1]
	if err := os.WriteFile(newest, []byte("not a zip archive at all"), 0o600); err != nil {
		t.Fatalf("corrupt %s: %v", newest, err)
	}

	var log bytes.Buffer
	e := newEngine()
	_, err = wireAndRehydrate(context.Background(), e, disk, city, &log)
	if err == nil {
		t.Fatalf("restore SUCCEEDED with a corrupt latest snapshot — fail-open. log=%q", log.String())
	}
	if !strings.Contains(err.Error(), "rehydrate city") {
		t.Fatalf("error is not the rehydrate wrapper: %v", err)
	}
	if strings.Contains(log.String(), "full genesis replay") {
		t.Fatalf("restore silently fell back to genesis replay despite a corrupt snapshot: %q", log.String())
	}
	if got := attackJournalFrameCount(t, dir, cityID); got != framesBefore {
		t.Fatalf("failed restore changed the journal: %d frames vs %d", got, framesBefore)
	}
}

// ---------------------------------------------------------------------------
// ATTACK 4 — cross-city snapshot isolation
// ---------------------------------------------------------------------------

// TestAttackInc3b_ForeignCitySnapshotNotUsed drives city A to several snapshots
// on a SHARED store, then restores a DIFFERENT city B from the same store. B
// must restore from its own (empty) records via genesis, never adopt A's
// snapshot — a keying bug here would silently hand one tenant another's city.
func TestAttackInc3b_ForeignCitySnapshotNotUsed(t *testing.T) {
	const cadence = int64(4)
	dir := t.TempDir()
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	cityA := persist.CityKey{TenantID: persistTenantID, CityID: "alpha"}
	cityB := persist.CityKey{TenantID: persistTenantID, CityID: "bravo"}

	digestA, tickA := attackDriveTicks(t, disk, cityA, cadence, 2*cadence+1)
	if tickA == 0 {
		t.Fatal("city A never advanced — harness broken")
	}
	idsA, err := disk.ListSnapshots(context.Background(), cityA)
	if err != nil {
		t.Fatalf("ListSnapshots(A): %v", err)
	}
	if len(idsA) < 2 {
		t.Fatalf("city A has %d snapshots, want >=2", len(idsA))
	}

	// B has never been touched: no journal, no snapshots.
	idsB, err := disk.ListSnapshots(context.Background(), cityB)
	if err != nil {
		t.Fatalf("ListSnapshots(B): %v", err)
	}
	if len(idsB) != 0 {
		t.Fatalf("fresh city B already has %d snapshots — store keying leaks across cities", len(idsB))
	}

	var log bytes.Buffer
	eB := newEngine()
	compB, err := wireAndRehydrate(context.Background(), eB, disk, cityB, &log)
	if err != nil {
		t.Fatalf("wireAndRehydrate(B): %v", err)
	}
	if got := compB.StateDigest(); got == digestA {
		t.Fatalf("fresh city B restored to city A's digest %x — foreign snapshot adopted", got)
	}
	clockB, err := eB.Clock()
	if err != nil {
		t.Fatalf("Clock(B): %v", err)
	}
	if clockB.Tick() != 0 {
		t.Fatalf("fresh city B restored to tick %d, want 0", clockB.Tick())
	}
	if !strings.Contains(log.String(), "starting fresh") {
		t.Fatalf("city B's restore log did not report a fresh start: %q", log.String())
	}
}

// ---------------------------------------------------------------------------
// ATTACK 5 — concurrent transport producers racing the snapshot hook
// ---------------------------------------------------------------------------

// TestAttackInc3b_ConcurrentProducersRestoreExact is the consistency attack the
// increment exists to survive. The builder's test sends every command
// synchronously from ONE goroutine, so it never exercises the real deployment
// shape: wsserver's connection goroutines and tickLoop are INDEPENDENT
// producers into the same transport. Here gameplay commands are fired from
// several goroutines concurrently with the tick driver, so a gameplay command
// can be enqueued at any point relative to a cadence-boundary tick — including
// in the window between that tick's HandleCommand/journal-append and its
// snapshot write.
//
// If the snapshot could observe engine state that the journal tail does not
// account for (or vice versa), the restored digest would diverge from the live
// one. Run with -race to also catch state read concurrently with the snapshot's
// Save.
func TestAttackInc3b_ConcurrentProducersRestoreExact(t *testing.T) {
	const cadence = int64(3)
	const ticks = int64(24)
	const producers = 4

	dir := t.TempDir()
	cityID := "concurrent"
	city := persist.CityKey{TenantID: persistTenantID, CityID: cityID}
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}

	e := newEngine()
	comp, err := wireAndRehydrate(context.Background(), e, disk, city, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("wireAndRehydrate (live): %v", err)
	}
	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	ctx, cancel := context.WithCancel(context.Background())
	loopDone := startCommandLoop(ctx, e, transport, comp, disk, city, cadence, attackTickCorrelationID, &bytes.Buffer{})

	// Single results drain (mirrors wsserver's sole pump goroutine).
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for range transport.Results() {
		}
	}()

	var wg sync.WaitGroup
	// Concurrent gameplay producers on their OWN correlation IDs.
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := 0; i < 8; i++ {
				cell := protocol.CellRef{X: p + 1, Y: i + 1}
				_ = transport.SendCommand(protocol.Command{
					ProtocolVersion: protocol.ProtocolVersion,
					CorrelationID:   protocol.CorrelationID(fmt.Sprintf("attack-gameplay-%d-%d", p, i)),
					Kind:            protocol.KindBuy,
					Payload:         protocol.BuyPayload{Cell: cell},
				})
			}
		}(p)
	}
	// The tick driver, concurrent with them all.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := int64(0); i < ticks; i++ {
			_ = transport.SendCommand(protocol.Command{
				ProtocolVersion: protocol.ProtocolVersion,
				CorrelationID:   protocol.CorrelationID(attackTickCorrelationID),
				Kind:            protocol.KindAdvanceTicks,
				Payload:         protocol.AdvanceTicksPayload{N: 1},
			})
		}
	}()
	wg.Wait()

	// Quiesce: the command loop is the sole consumer, so once it has drained
	// the command channel every command above has been handled and journaled.
	for len(transport.Commands()) > 0 {
	}
	cancel()
	if err := <-loopDone; err != nil {
		t.Fatalf("RunCommandLoop: %v", err)
	}
	liveDigest := comp.StateDigest()
	_ = transport.Close()
	<-drained

	ids, err := disk.ListSnapshots(context.Background(), city)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(ids) == 0 {
		t.Fatal("no snapshots taken during the concurrent run — the attack never reached a cadence boundary")
	}

	// Restore into a fresh engine from the same durable store.
	var log bytes.Buffer
	e2 := newEngine()
	comp2, err := wireAndRehydrate(context.Background(), e2, disk, city, &log)
	if err != nil {
		t.Fatalf("wireAndRehydrate (restore): %v — log=%q", err, log.String())
	}
	if got := comp2.StateDigest(); got != liveDigest {
		t.Fatalf("snapshot+tail restore diverged under concurrent producers:\n  live     = %x\n  restored = %x\n  log=%q",
			liveDigest, got, log.String())
	}
	if !strings.Contains(log.String(), "from latest snapshot + journal tail") {
		t.Fatalf("restore did not use the snapshot path: %q", log.String())
	}
	// And a second restart must not grow the journal.
	frames := attackJournalFrameCount(t, dir, cityID)
	e3 := newEngine()
	comp3, err := wireAndRehydrate(context.Background(), e3, disk, city, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("wireAndRehydrate (restart 2): %v", err)
	}
	if got := comp3.StateDigest(); got != liveDigest {
		t.Fatalf("second restart diverged: %x != %x", got, liveDigest)
	}
	if got := attackJournalFrameCount(t, dir, cityID); got != frames {
		t.Fatalf("second restart grew the journal: %d vs %d frames", got, frames)
	}
}

// ---------------------------------------------------------------------------
// ATTACK 6 — BUG-472 journal-swallow vs. a snapshot at the same boundary
// ---------------------------------------------------------------------------

// TestAttackInc3b_SwallowedJournalAppendAtBoundary is BUG-480's flipped
// regression: it used to DOCUMENT that a swallowed journal append (BUG-472's
// policy) landing at the very tick a snapshot cadence boundary fires could
// leave a snapshot recorded AHEAD of what the journal's AdvanceTicks frames
// sum to, bricking every future restore for that city with a PERMANENT
// ErrSnapshotTailShort (compose.RestoreLatestSnapshotOrGenesis always picked
// the newest snapshot with no fallback).
//
// BUG-480 shipped two complementary fixes and this test now proves BOTH,
// live, through the real production wiring (startCommandLoop /
// wireAndRehydrate — no compose-package white-box access):
//
//  1. deliverable (b), the JOURNAL-DIRTY GATE: MaybeSnapshotEvery refuses to
//     write a snapshot once its journaler has observed a failed durable
//     append (persistCommandJournaler.dirty) — including at the EXACT tick
//     the failure itself occurred, since the gate is checked synchronously
//     on the same goroutine right after journalAccepted's swallow. This
//     closes the specific live race the pre-fix test documented: the
//     scenario "a bad snapshot gets written at the very boundary the
//     swallow happens on" can no longer arise in-process, so this test
//     asserts NO snapshot is ever taken for this city (ids stays empty)
//     rather than skipping when that turns out to be the case.
//  2. deliverable (a), WALK-BACK: even with (b) closing the live race, a
//     restart must still restore correctly with NO durable snapshot at all
//     for this city (RestoreLatestSnapshotOrGenesis's pre-existing
//     genesis-replay fallback, unaffected by BUG-480) — proving the fix
//     did not regress the always-worked path while it was busy repairing
//     the always-broken one.
func TestAttackInc3b_SwallowedJournalAppendAtBoundary(t *testing.T) {
	const cadence = int64(4)
	dir := t.TempDir()
	city := persist.CityKey{TenantID: persistTenantID, CityID: "swallow"}
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	// Fail the 4th append: the AdvanceTicks that lands exactly on tick 4, the
	// first cadence boundary. Its state effect happens (BUG-472, untouched);
	// its journal frame does not — and BUG-480's dirty gate must now refuse
	// the tick-4 snapshot itself, not just some later one.
	failing := &attackFailingAppendStore{Store: disk, failCall: 4}

	liveDigest, tick := attackDriveTicks(t, failing, city, cadence, 2*cadence)
	if tick != 2*cadence {
		t.Fatalf("engine tick %d, want %d", tick, 2*cadence)
	}
	ids, err := disk.ListSnapshots(context.Background(), city)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(ids) != 0 {
		t.Fatalf("BUG-480 deliverable (b) regressed: %d durable snapshot(s) exist for a city whose journaler observed a failed append -- the dirty gate did not hold", len(ids))
	}

	// The honest reference: genesis replay of exactly what persisted (the
	// swallowed frame is genuinely, permanently lost -- BUG-472's policy,
	// untouched by this fix).
	persistedCmds, err := compose.RestoreCommands(context.Background(), disk, city)
	if err != nil {
		t.Fatalf("RestoreCommands: %v", err)
	}
	eRef := newEngine()
	compRef, err := compose.Wire(eRef, nil)
	if err != nil {
		t.Fatalf("Wire (reference): %v", err)
	}
	for i, cmd := range persistedCmds {
		if res := eRef.HandleCommand(cmd); !res.Accepted {
			t.Fatalf("reference replay: command %d (%s) rejected: %+v", i, cmd.Kind, res.Error)
		}
	}
	refClock, err := eRef.Clock()
	if err != nil {
		t.Fatalf("Clock (reference): %v", err)
	}
	if refClock.Tick() != tick-1 {
		t.Fatalf("precondition: reference (persisted-journal-only) tick = %d, want %d (one tick short of live %d -- the swallowed frame)", refClock.Tick(), tick-1, tick)
	}
	if compRef.StateDigest() == liveDigest {
		t.Fatal("precondition: reference digest equals the live digest -- the swallowed command carried no observable effect, weakening this test's fault injection")
	}

	// Restart: no durable snapshot exists at all, so restore MUST use the
	// pre-existing genesis-replay fallback (usedSnapshot=false) and must
	// reproduce the reference EXACTLY -- never the live digest, which
	// included the lost command's effect and can never be honestly
	// reconstructed from what actually persisted.
	var log bytes.Buffer
	e := newEngine()
	comp, err := wireAndRehydrate(context.Background(), e, disk, city, &log)
	if err != nil {
		t.Fatalf("wireAndRehydrate: restore with NO durable snapshot must always succeed via genesis replay, got: %v — log=%q", err, log.String())
	}
	if !strings.Contains(log.String(), "full genesis replay") {
		t.Fatalf("restore did not report the genesis-replay path: %q", log.String())
	}
	restoredClock, err := e.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	if restoredClock.Tick() != refClock.Tick() {
		t.Fatalf("restored tick = %d, want %d (reference)", restoredClock.Tick(), refClock.Tick())
	}
	if comp.StateDigest() != compRef.StateDigest() {
		t.Fatalf("restored digest = %x, want %x (reference) -- restore must honestly reproduce only what persisted", comp.StateDigest(), compRef.StateDigest())
	}
}

// ---------------------------------------------------------------------------
// ATTACK 7 — CityHost: 3 cities snapshotting, clean shutdown, no goroutine leak
// ---------------------------------------------------------------------------

// TestAttackInc3b_CityHostThreeCitiesSnapshotAndShutdown drives three cities on
// ONE CityHost (one shared DiskStore) with snapshotting on, closes the host
// while ticks are still flowing (so a snapshot write can be in flight), and
// asserts: every city took its own snapshots, each city's snapshots are keyed
// to itself, Close returns cleanly, and no goroutines leak.
func TestAttackInc3b_CityHostThreeCitiesSnapshotAndShutdown(t *testing.T) {
	before := runtime.NumGoroutine()

	dir := t.TempDir()
	h, err := NewCityHost(dir, time.Millisecond, WithSnapshotEvery(2))
	if err != nil {
		t.Fatalf("NewCityHost: %v", err)
	}
	h.engineOpts = testEngineOpts()

	keys := []persist.CityKey{
		{TenantID: persistTenantID, CityID: "host-a"},
		{TenantID: persistTenantID, CityID: "host-b"},
		{TenantID: persistTenantID, CityID: "host-c"},
	}
	for _, k := range keys {
		if _, err := h.GetOrCreate(context.Background(), k); err != nil {
			t.Fatalf("GetOrCreate(%s): %v", k.CityID, err)
		}
	}

	// Let the 1ms tick drivers cross several cadence-2 boundaries, so Close
	// below lands with snapshot writes plausibly in flight.
	deadline := time.Now().Add(10 * time.Second)
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	// Wait until EVERY city has snapshotted at least twice, so Close below is a
	// genuine mid-flight shutdown for all three rather than a race with the
	// last-created city's very first tick.
	allReady := func() bool {
		for _, k := range keys {
			ids, err := disk.ListSnapshots(context.Background(), k)
			if err != nil {
				t.Fatalf("ListSnapshots(%s): %v", k.CityID, err)
			}
			if len(ids) < 2 {
				return false
			}
		}
		return true
	}
	for time.Now().Before(deadline) && !allReady() {
		time.Sleep(5 * time.Millisecond)
	}
	if !allReady() {
		t.Fatal("not every city reached 2 snapshots within the deadline - WithSnapshotEvery did not reach every buildCity")
	}

	if err := h.Close(); err != nil {
		t.Fatalf("Close (mid-snapshot): %v", err)
	}

	// Every city snapshotted independently under its own key.
	for _, k := range keys {
		ids, err := disk.ListSnapshots(context.Background(), k)
		if err != nil {
			t.Fatalf("ListSnapshots(%s): %v", k.CityID, err)
		}
		if len(ids) == 0 {
			t.Fatalf("city %s took no snapshots — WithSnapshotEvery did not reach buildCity", k.CityID)
		}
		if len(ids) > compose.MaxRetainedSnapshots {
			t.Fatalf("city %s retained %d snapshots, above the %d bound — pruning did not run",
				k.CityID, len(ids), compose.MaxRetainedSnapshots)
		}
	}

	// Every city must still restore exactly after the abrupt shutdown.
	for _, k := range keys {
		var log bytes.Buffer
		// BUG-479: the restore engine must carry the SAME world seed the city
		// was created with (seedForCity, as buildCity does) — the fixed
		// inc4Seed helper was a differently-seeded restore that only passed
		// because nothing validated the bundle seed before BUG-479.
		e := core.NewEngine(core.WithWorldSeed(seedForCity(k)), core.WithPoolSize(1))
		if _, err := wireAndRehydrate(context.Background(), e, disk, k, &log); err != nil {
			t.Fatalf("restore %s after mid-snapshot Close: %v — log=%q", k.CityID, err, log.String())
		}
	}

	// Goroutine leak check: allow a small settle window for runtime teardown.
	for i := 0; i < 50; i++ {
		if runtime.NumGoroutine() <= before+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutine leak after Close: %d before, %d after", before, runtime.NumGoroutine())
}

// ---------------------------------------------------------------------------
// ATTACK 8 — determinism: two identical runs produce identical digests
// ---------------------------------------------------------------------------

// TestAttackInc3b_SnapshotPathIsDeterministic runs the SAME driven workload
// twice into two separate stores and asserts both the live and the restored
// digests are identical across runs (GR#21). Any map-iteration or wall-clock
// dependence introduced into the snapshot pack/restore path would break this.
func TestAttackInc3b_SnapshotPathIsDeterministic(t *testing.T) {
	const cadence = int64(4)
	const ticks = int64(2*cadence + 3)

	run := func() (live, restored [32]byte) {
		dir := t.TempDir()
		city := persist.CityKey{TenantID: persistTenantID, CityID: "det"}
		disk, err := persist.NewDiskStore(dir)
		if err != nil {
			t.Fatalf("NewDiskStore: %v", err)
		}
		live, _ = attackDriveTicks(t, disk, city, cadence, ticks)
		e := newEngine()
		comp, err := wireAndRehydrate(context.Background(), e, disk, city, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("wireAndRehydrate: %v", err)
		}
		return live, comp.StateDigest()
	}

	live1, restored1 := run()
	live2, restored2 := run()

	if live1 != live2 {
		t.Fatalf("live digests differ across identical runs: %x vs %x", live1, live2)
	}
	if restored1 != restored2 {
		t.Fatalf("restored digests differ across identical runs: %x vs %x", restored1, restored2)
	}
	if live1 != restored1 {
		t.Fatalf("restore not exact: live %x vs restored %x", live1, restored1)
	}
}

// ---------------------------------------------------------------------------
// ATTACK 9 — restart with a snapshot whose journal tail is EMPTY
// ---------------------------------------------------------------------------

// TestAttackInc3b_EmptyTailRestore stops the city on an EXACT cadence boundary,
// so the newest snapshot's tick equals the final journal tick and
// splitJournalAtTick must return a zero-length tail. This is the boundary case
// where an off-by-one in the "running+payload.N == snapshotTick" branch would
// either replay the boundary AdvanceTicks a second time (tick overshoot) or
// drop it. The restored digest and tick must both match the live ones exactly,
// and a second restart must not grow the journal.
func TestAttackInc3b_EmptyTailRestore(t *testing.T) {
	const cadence = int64(4)
	const ticks = 3 * cadence // lands EXACTLY on a boundary -> empty tail

	dir := t.TempDir()
	cityID := "emptytail"
	city := persist.CityKey{TenantID: persistTenantID, CityID: cityID}
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}

	liveDigest, liveTick := attackDriveTicks(t, disk, city, cadence, ticks)
	if liveTick != ticks {
		t.Fatalf("live tick %d, want %d", liveTick, ticks)
	}

	var log bytes.Buffer
	e := newEngine()
	comp, err := wireAndRehydrate(context.Background(), e, disk, city, &log)
	if err != nil {
		t.Fatalf("wireAndRehydrate: %v - log=%q", err, log.String())
	}
	if !strings.Contains(log.String(), "from latest snapshot + journal tail") {
		t.Fatalf("empty-tail restore did not take the snapshot path: %q", log.String())
	}
	clock, err := e.Clock()
	if err != nil {
		t.Fatalf("Clock: %v", err)
	}
	if clock.Tick() != liveTick {
		t.Fatalf("empty-tail restore tick %d != live tick %d (over/under-applied the boundary command)", clock.Tick(), liveTick)
	}
	if got := comp.StateDigest(); got != liveDigest {
		t.Fatalf("empty-tail restore digest %x != live %x", got, liveDigest)
	}

	frames := attackJournalFrameCount(t, dir, cityID)
	e2 := newEngine()
	comp2, err := wireAndRehydrate(context.Background(), e2, disk, city, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("wireAndRehydrate (restart 2): %v", err)
	}
	if got := comp2.StateDigest(); got != liveDigest {
		t.Fatalf("second empty-tail restart diverged: %x != %x", got, liveDigest)
	}
	if got := attackJournalFrameCount(t, dir, cityID); got != frames {
		t.Fatalf("second empty-tail restart grew the journal: %d vs %d", got, frames)
	}
}
