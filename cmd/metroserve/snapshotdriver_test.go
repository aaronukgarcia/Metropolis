package main

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/persist"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-1972079936 Phase 1 inc3b — wiring compose's snapshot cadence into
// metroserve's live tick driver (snapshotdriver.go), and switching
// wireAndRehydrate (persist.go) over to the snapshot-aware restore path
// (compose.RestoreLatestSnapshotOrGenesis).
//
// This file proves the headline claim: a durable snapshot taken by the
// live tick driver, at a tick boundary consistent with the journal
// position, restores EXACTLY (StateDigest-equal) via "latest snapshot +
// journal tail" — NOT a full genesis replay — and that the restart-guard
// (double-append suppression) still holds across repeated restarts, now
// that restore goes through the snapshot-aware path instead of the old
// always-genesis one.

// inc3bCadence is deliberately small (not the production 360) so the test
// crosses several cadence boundaries in a handful of ticks without needing
// a slow test.
const inc3bCadence = int64(4)

// countingStore wraps a persist.Store and counts calls to GetSnapshot and
// ReadJournal, so a test can assert restore actually CONSULTED the durable
// snapshot store (proof it did not silently fall back to genesis replay)
// rather than trusting the digest match alone (a digest match is necessary
// but not sufficient — a correct-but-slow genesis replay would produce the
// identical digest too; the call counts are what distinguish "used the
// snapshot" from "ignored it and replayed everything").
type countingStore struct {
	persist.Store
	getSnapshotCalls int64
	readJournalCalls int64
}

func (c *countingStore) GetSnapshot(ctx context.Context, city persist.CityKey, id persist.SnapshotID) ([]byte, error) {
	atomic.AddInt64(&c.getSnapshotCalls, 1)
	return c.Store.GetSnapshot(ctx, city, id)
}

func (c *countingStore) ReadJournal(ctx context.Context, city persist.CityKey) ([][]byte, error) {
	atomic.AddInt64(&c.readJournalCalls, 1)
	return c.Store.ReadJournal(ctx, city)
}

// sendAndAwait submits cmd through transport and blocks for its
// CommandResult, failing the test on a 5s stall (a safety net against a
// genuine deadlock bug, NOT a performance/latency assertion — GR#21: no
// wall-clock upper bound is asserted as a pass/fail correctness condition,
// this only bounds how long a broken test may hang the suite).
func sendAndAwait(t *testing.T, transport *protocol.InProcTransport, cmd protocol.Command) protocol.CommandResult {
	t.Helper()
	if err := transport.SendCommand(cmd); err != nil {
		t.Fatalf("SendCommand(%s, %s): %v", cmd.CorrelationID, cmd.Kind, err)
	}
	select {
	case res := <-transport.Results():
		if res.CorrelationID != cmd.CorrelationID {
			t.Fatalf("result correlation mismatch: sent %s, got %s", cmd.CorrelationID, res.CorrelationID)
		}
		return res
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for result of %s (%s)", cmd.CorrelationID, cmd.Kind)
		return protocol.CommandResult{}
	}
}

// TestSnapshotDriver_TailRestoreExactAndGuardHolds is deliverable 3's proof:
//
//  1. Drive a live city N (> 2*cadence) ticks through startCommandLoop's
//     REAL wiring (transport -> RunCommandLoop wrapped by
//     snapshotCommandSource), interleaving gameplay commands (Buy/Zone)
//     with a DIFFERENT correlation ID. NOTE (independent round r1): this
//     does NOT prove the correlation-ID gate in
//     snapshotCommandSource.SendResult discriminates — deleting the gate
//     leaves this suite green, because MaybeSnapshotEvery re-checks the
//     cadence itself and a redundant snapshot is byte-identical. The gate
//     is a write-cost optimisation, not a correctness boundary
//     (BUG-481 tracks the client-forgeable-ID surface).
//  2. Restart: wireAndRehydrate a FRESH engine from the SAME on-disk store
//     (wrapped in countingStore) — the exact seam CityHost/setUpPersistence
//     both call (GR#3, single-sourced).
//  3. The restored StateDigest matches the live one EXACTLY.
//  4. The restore genuinely consulted the snapshot store (GetSnapshot
//     called, and wireAndRehydrate's own informative stdout line says
//     "from latest snapshot + journal tail", never "full genesis replay")
//     — proof it did not silently fall back to full genesis replay despite
//     snapshots existing.
//  5. Restarting FOUR more times (through the production setUpPersistence
//     seam, matching attack_inc4_test.go's existing double-append attack)
//     never grows the on-disk journal frame count — the rehydrate guard's
//     re-append suppression still holds now that restore goes through the
//     snapshot-aware path.
func TestSnapshotDriver_TailRestoreExactAndGuardHolds(t *testing.T) {
	dir := t.TempDir()
	cityID := "inc3b"
	cityKey := persist.CityKey{TenantID: persistTenantID, CityID: cityID}
	totalTicks := int64(2*inc3bCadence + 5) // > 2*cadence, per deliverable 3(a)

	// --- 1. Live run through the REAL tick-driver wiring.
	eA := newEngine()
	compA, storeA, err := setUpPersistence(eA, dir, cityID, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("setUpPersistence (live): %v", err)
	}
	if storeA == nil {
		t.Fatal("persist on: setUpPersistence returned a nil Store")
	}

	transport := protocol.NewInProcTransport(
		protocol.DefaultCommandBuffer, protocol.DefaultResultBuffer,
		protocol.DefaultEventBuffer, protocol.DefaultDeltaBuffer,
	)
	ctx, cancel := context.WithCancel(context.Background())
	tickCorrelationID := "inc3b-tick-driver"
	loopDone := startCommandLoop(ctx, eA, transport, compA, storeA, cityKey, inc3bCadence, tickCorrelationID, &bytes.Buffer{})

	cell := protocol.CellRef{X: 4, Y: 4}
	for i := int64(1); i <= totalTicks; i++ {
		// Interleave gameplay commands under a DIFFERENT correlation ID —
		// these must NEVER trigger onTickResult (proving the correlation-ID
		// gate discriminates, not just "any accepted command triggers a
		// snapshot").
		switch i {
		case 2:
			sendAndAwait(t, transport, protocol.Command{
				ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "inc3b-buy",
				Kind: protocol.KindBuy, Payload: protocol.BuyPayload{Cell: cell},
			})
		case 3:
			sendAndAwait(t, transport, protocol.Command{
				ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "inc3b-zone",
				Kind: protocol.KindZone, Payload: protocol.ZonePayload{Cell: cell, ZoneType: "dwelling"},
			})
		case 5:
			sendAndAwait(t, transport, protocol.Command{
				ProtocolVersion: protocol.ProtocolVersion, CorrelationID: "inc3b-build",
				Kind: protocol.KindBuild, Payload: protocol.BuildPayload{Cell: cell, BuildingType: "dwelling"},
			})
		}
		res := sendAndAwait(t, transport, protocol.Command{
			ProtocolVersion: protocol.ProtocolVersion,
			CorrelationID:   protocol.CorrelationID(tickCorrelationID),
			Kind:            protocol.KindAdvanceTicks,
			Payload:         protocol.AdvanceTicksPayload{N: 1},
		})
		if !res.Accepted {
			t.Fatalf("tick %d rejected: %+v", i, res.Error)
		}
	}
	liveDigest := compA.StateDigest()
	liveTick := int64(0)
	if c, err := eA.Clock(); err == nil {
		liveTick = c.Tick()
	}
	if liveTick != totalTicks {
		t.Fatalf("engine tick %d != commands driven %d — the live-run harness itself is broken", liveTick, totalTicks)
	}

	cancel()
	if err := <-loopDone; err != nil {
		t.Fatalf("RunCommandLoop exited with error: %v", err)
	}
	_ = transport.Close()

	// Sanity: at least two cadence boundaries (tick inc3bCadence, 2*inc3bCadence)
	// were crossed by totalTicks, so at least 2 snapshots must exist.
	plainDisk, err := persist.NewDiskStore(dir)
	if err != nil {
		t.Fatalf("NewDiskStore (post-run inspection): %v", err)
	}
	ids, err := plainDisk.ListSnapshots(context.Background(), cityKey)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(ids) < 2 {
		t.Fatalf("expected >=2 durable snapshots after %d ticks at cadence %d, got %d — the tick driver never fired MaybeSnapshotEvery", totalTicks, inc3bCadence, len(ids))
	}

	// --- 2. Restart from the SAME store, through the single-sourced
	// wireAndRehydrate seam, wrapped in countingStore.
	counting := &countingStore{Store: plainDisk}
	eB := newEngine()
	var restoreLog bytes.Buffer
	compB, err := wireAndRehydrate(context.Background(), eB, counting, cityKey, &restoreLog)
	if err != nil {
		t.Fatalf("wireAndRehydrate (restart): %v", err)
	}

	// --- 3. Exact digest match.
	restoredDigest := compB.StateDigest()
	if restoredDigest != liveDigest {
		t.Fatalf("restored digest %x != live digest %x — snapshot+tail restore is not exact", restoredDigest, liveDigest)
	}

	// --- 4. Restore genuinely consulted the snapshot (did not silently fall
	// back to full genesis replay despite snapshots existing on disk).
	if atomic.LoadInt64(&counting.getSnapshotCalls) == 0 {
		t.Fatal("restore never called GetSnapshot despite snapshots existing on disk — fell back to genesis replay")
	}
	logMsg := restoreLog.String()
	if !strings.Contains(logMsg, "from latest snapshot + journal tail") {
		t.Fatalf("restore's own log line did not report the snapshot-aware path: %q", logMsg)
	}
	if strings.Contains(logMsg, "full genesis replay") {
		t.Fatalf("restore's own log line reports a full genesis replay despite snapshots existing: %q", logMsg)
	}

	// --- 5. Restart FOUR more times through the production seam
	// (setUpPersistence) — the journal frame count must never grow (the
	// rehydrate guard's re-append suppression still holds).
	baseFrames := attackJournalFrameCount(t, dir, cityID)
	prevDigest := restoredDigest
	for i, label := range []string{"C", "D", "E", "F"} {
		e := newEngine()
		comp, _, err := setUpPersistence(e, dir, cityID, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("setUpPersistence (%s): %v", label, err)
		}
		d := comp.StateDigest()
		if d != prevDigest {
			t.Fatalf("restart %s (#%d) diverged: %x != %x", label, i+3, d, prevDigest)
		}
		frames := attackJournalFrameCount(t, dir, cityID)
		if frames != baseFrames {
			t.Fatalf("restart %s grew the journal: %d frames vs %d after the first restore (double-append)", label, frames, baseFrames)
		}
		prevDigest = d
	}
}
