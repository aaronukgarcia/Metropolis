package main

import (
	"bytes"
	"context"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// journalFrameCount reads the durable journal for a city and returns how many
// frames it holds — the direct on-disk measure the double-append bug would
// grow across restarts.
func attackJournalFrameCount(t *testing.T, dir, cityID string) int {
	t.Helper()
	disk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore: %v", err)
	}
	frames, err := disk.ReadJournal(context.Background(), persist.CityKey{TenantID: persistTenantID, CityID: cityID})
	if err != nil {
		t.Fatalf("ReadJournal: %v", err)
	}
	return len(frames)
}

// TestAttack_RestartFourTimes_NoJournalGrowth is the P0: persist a city, then
// rehydrate it FOUR times, asserting StateDigest is identical across every
// restart AND the on-disk journal frame count never grows. The double-append
// bug (guard failing to suppress re-appends during replay) would grow the
// journal on each rehydrate and diverge the digest.
func TestAttack_RestartFourTimes_NoJournalGrowth(t *testing.T) {
	dir := t.TempDir()

	// A: fresh boot, submit the deterministic sequence.
	eA := newEngine()
	compA, storeA, err := setUpPersistence(eA, dir, "rt4", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("setUpPersistence A: %v", err)
	}
	if storeA == nil {
		t.Fatal("persist on returned nil store")
	}
	submitAll(t, eA, rtCommands())
	digestA := compA.StateDigest()

	framesAfterA := attackJournalFrameCount(t, dir, "rt4")
	if framesAfterA == 0 {
		t.Fatal("no journal frames written after A — the round-trip cannot be meaningful")
	}

	// Restart 3 more times (B, C, D). Digest must equal A each time; frame
	// count must be exactly framesAfterA each time (no growth).
	prevDigest := digestA
	for i, label := range []string{"B", "C", "D"} {
		e := newEngine()
		comp, _, err := setUpPersistence(e, dir, "rt4", &bytes.Buffer{})
		if err != nil {
			t.Fatalf("setUpPersistence %s: %v", label, err)
		}
		d := comp.StateDigest()
		if d != prevDigest {
			t.Fatalf("restart %s (#%d) diverged: %x != %x (journal double-append / lossy replay?)", label, i+2, d, prevDigest)
		}
		frames := attackJournalFrameCount(t, dir, "rt4")
		if frames != framesAfterA {
			t.Fatalf("restart %s grew the journal: %d frames vs %d after A (double-append)", label, frames, framesAfterA)
		}
		prevDigest = d
	}
}

// TestAttack_LiveCommandsAfterRehydrate_JournaledOnce is the subtle variant of
// the double-append attack: after a rehydrate, the guard must be in
// pass-through mode so LIVE commands ARE journaled — exactly once — and a
// subsequent restart replays the grown journal losslessly without doubling.
// This catches a guard that suppressed the first restart but mis-handles
// commands submitted AFTER rehydrate + another restart.
func TestAttack_LiveCommandsAfterRehydrate_JournaledOnce(t *testing.T) {
	dir := t.TempDir()

	// A: base sequence.
	eA := newEngine()
	_, _, err := setUpPersistence(eA, dir, "live", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("A: %v", err)
	}
	base := rtCommands()
	submitAll(t, eA, base)
	framesA := attackJournalFrameCount(t, dir, "live")

	// B: rehydrate, THEN submit 3 more live commands (must be journaled once).
	eB := newEngine()
	compB, _, err := setUpPersistence(eB, dir, "live", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("B: %v", err)
	}
	live := []protocol.Command{
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "live-1", Kind: protocol.KindAdvanceTicks, Payload: protocol.AdvanceTicksPayload{N: 3}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "live-2", Kind: protocol.KindAdvanceTicks, Payload: protocol.AdvanceTicksPayload{N: 7}},
		{ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "live-3", Kind: protocol.KindAdvanceTicks, Payload: protocol.AdvanceTicksPayload{N: 2}},
	}
	submitAll(t, eB, live)
	digestB := compB.StateDigest()

	framesB := attackJournalFrameCount(t, dir, "live")
	if framesB != framesA+len(live) {
		t.Fatalf("live commands after rehydrate journaled wrong count: %d frames, expected %d (%d base + %d live) — replay re-appended or live drop", framesB, framesA+len(live), framesA, len(live))
	}

	// C: restart again. Must rehydrate to B's digest (base+live), no doubling.
	eC := newEngine()
	compC, _, err := setUpPersistence(eC, dir, "live", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("C: %v", err)
	}
	if compC.StateDigest() != digestB {
		t.Fatalf("restart after live commands diverged: %x != %x", compC.StateDigest(), digestB)
	}
	framesC := attackJournalFrameCount(t, dir, "live")
	if framesC != framesB {
		t.Fatalf("restart after live commands grew journal: %d vs %d (double-append)", framesC, framesB)
	}
}
