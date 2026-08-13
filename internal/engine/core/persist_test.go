package core

import (
	"bytes"
	"testing"
	"time"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

func TestSnapshot_RoundTripsThroughSerialize(t *testing.T) {
	e := NewEngine(WithWorldSeed(7))
	if err := e.AdvanceTicks("corr-snap", 40); err != nil {
		t.Fatalf("AdvanceTicks: %v", err)
	}

	var buf bytes.Buffer
	header, err := e.Snapshot(&buf, "corr-snap-2")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if header.WorldSeed != 7 {
		t.Errorf("header.WorldSeed = %d, want 7", header.WorldSeed)
	}
	if header.CreatedAtTick != 40 {
		t.Errorf("header.CreatedAtTick = %d, want 40", header.CreatedAtTick)
	}
	if header.GameMonth != 1 {
		t.Errorf("header.GameMonth = %d, want 1", header.GameMonth)
	}
	if len(header.ShardIndex) != 1 || header.ShardIndex[0].Name != "meta" {
		t.Fatalf("header.ShardIndex = %+v, want one shard named %q", header.ShardIndex, "meta")
	}

	var records []serialize.Record
	err = (serialize.NDJSONSerializer{}).ReadShard(&buf, 0, func(r serialize.Record) error {
		records = append(records, r)
		return nil
	})
	if err != nil {
		t.Fatalf("ReadShard: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	if records[0].Kind != "meta" {
		t.Errorf("records[0].Kind = %q, want %q", records[0].Kind, "meta")
	}
}

// TestSnapshot_NewSaveHasNoDebugTouched is SEC-027 regression case 1: a
// genuinely new save (no prior header passed at all — the existing
// two-arg call shape) still gets debugTouched=false, exactly as before
// this fix. Proves the fix did not accidentally start defaulting to
// true or otherwise change new-save behaviour.
func TestSnapshot_NewSaveHasNoDebugTouched(t *testing.T) {
	e := NewEngine(WithWorldSeed(1))

	var buf bytes.Buffer
	header, err := e.Snapshot(&buf, "corr-new-save")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if header.DebugTouched() {
		t.Fatal("header.DebugTouched() = true for a genuinely new save with no prior header, want false")
	}
}

// TestSnapshot_SaveOverCarriesDebugTouchedForward is SEC-027 regression
// case 2: a save-over of an existing debug-touched save (simulated by
// passing a prior header with TouchDebug already called, as a save-over
// caller reading the on-disk header would) must carry debugTouched=true
// forward into the fresh header Snapshot builds — proving the flag
// survives a save-over via serialize.Header.MergeDebugTouched, closing
// the exact gap SEC-027 flagged (NewHeader always starting false with
// nothing merging the prior flag forward at the call site).
func TestSnapshot_SaveOverCarriesDebugTouchedForward(t *testing.T) {
	e := NewEngine(WithWorldSeed(2))

	var prior serialize.Header
	prior.TouchDebug()
	if !prior.DebugTouched() {
		t.Fatal("setup: prior.DebugTouched() = false after TouchDebug(), want true")
	}

	var buf bytes.Buffer
	header, err := e.Snapshot(&buf, "corr-save-over-touched", prior)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !header.DebugTouched() {
		t.Fatal("header.DebugTouched() = false after a save-over of a debug-touched prior header, want true (SEC-027 regression)")
	}
}

// TestSnapshot_SaveOverOfCleanSaveStaysClean is SEC-027 regression case
// 3: a save-over of an existing NON-debug-touched save must stay
// debugTouched=false — MergeDebugTouched must never contaminate a clean
// save with a false positive, only carry forward a true prior flag.
func TestSnapshot_SaveOverOfCleanSaveStaysClean(t *testing.T) {
	e := NewEngine(WithWorldSeed(3))

	var prior serialize.Header // never TouchDebug'd — a clean prior save
	if prior.DebugTouched() {
		t.Fatal("setup: prior.DebugTouched() = true for an untouched Header, want false")
	}

	var buf bytes.Buffer
	header, err := e.Snapshot(&buf, "corr-save-over-clean", prior)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if header.DebugTouched() {
		t.Fatal("header.DebugTouched() = true after a save-over of a clean prior header, want false (false-positive contamination)")
	}
}

// blockingWriter blocks on unblock (a channel closed by the test) after
// writing the first chunk of bytes, so the test can prove AdvanceTicks
// keeps progressing while a Snapshot's write is still in flight — a
// channel/counter-based assertion (AC-8), never a wall-clock timing
// one.
type blockingWriter struct {
	inner     *bytes.Buffer
	started   chan struct{}
	unblock   chan struct{}
	firstDone bool
}

func (w *blockingWriter) Write(p []byte) (int, error) {
	if !w.firstDone {
		w.firstDone = true
		close(w.started)
		<-w.unblock
	}
	return w.inner.Write(p)
}

func TestSnapshot_DoesNotBlockTickLoop(t *testing.T) {
	e := NewEngine(WithWorldSeed(1))

	bw := &blockingWriter{
		inner:   &bytes.Buffer{},
		started: make(chan struct{}),
		unblock: make(chan struct{}),
	}

	snapshotDone := make(chan error, 1)
	go func() {
		_, err := e.Snapshot(bw, "corr-slow-snapshot")
		snapshotDone <- err
	}()

	select {
	case <-bw.started:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Snapshot's write to start")
	}

	// The snapshot's writer is now blocked mid-write, holding no Engine
	// lock (persist.go's snapshotStateLocked already returned). Prove
	// the tick loop is unaffected: AdvanceTicks must complete promptly.
	advanceDone := make(chan error, 1)
	go func() {
		advanceDone <- e.AdvanceTicks("corr-during-snapshot", 5)
	}()

	select {
	case err := <-advanceDone:
		if err != nil {
			t.Fatalf("AdvanceTicks while Snapshot write is blocked: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("AdvanceTicks did not complete while Snapshot's write was still blocked — T-PERSIST is blocking the tick loop (AC-8 violated)")
	}
	if got := e.TicksCompleted(); got != 5 {
		t.Errorf("TicksCompleted() = %d, want 5 (advanced while snapshot write was still blocked)", got)
	}

	close(bw.unblock)
	select {
	case err := <-snapshotDone:
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Snapshot to finish after unblocking its writer")
	}
}
