package save

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// BUG-157 regression suite: writeBundle is the REAL production save-over
// path (SaveManual/Autosave/Milestone all funnel through it), and must
// carry a prior on-disk header's DebugTouched flag forward via
// serialize.Header.MergeDebugTouched on a save-over, exactly the pattern
// SEC-027 established one layer up in engine.core's Snapshot. Every case
// here drives the REAL SaveManual entry point (never writeBundle
// directly), because BUG-157's whole point was that the production
// caller path was the gap SEC-027's persist.go fix did not cover.

// debugTouchedFixtureContext is fixtureContext plus an explicit
// DebugTouched value, so these tests can drive a debug-touched save
// through the real Context.DebugTouched -> header.TouchDebug() path
// (see writeBundle) rather than reaching into serialize internals.
func debugTouchedFixtureContext(tick, month int64, debugTouched bool) Context {
	ctx := fixtureContext(tick, month)
	ctx.DebugTouched = debugTouched
	return ctx
}

// TestWriteBundle_FirstTimeSaveNeverDebugTouched is BUG-157 regression
// case 1: a genuinely first-time save to an empty slot (finalDir does
// not exist yet) must get debugTouched=false when the save itself was
// never debug-touched — there is no prior header to merge, and the
// fresh header must not spuriously come up true.
func TestWriteBundle_FirstTimeSaveNeverDebugTouched(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, nil, "test-corr")

	if err := mgr.SaveManual(debugTouchedFixtureContext(1, 0, false), "slot-a"); err != nil {
		t.Fatalf("SaveManual (first-time): %v", err)
	}

	header, err := serialize.ReadHeader(manualDir(root, "slot-a"))
	if err != nil {
		t.Fatalf("ReadHeader: %v", err)
	}
	if header.DebugTouched() {
		t.Fatal("header.DebugTouched() = true for a genuinely first-time save with no prior header, want false")
	}
}

// TestWriteBundle_SaveOverCarriesDebugTouchedForward is BUG-157
// regression case 2: SaveManual first writes a debug-touched save to a
// slot, then SaveManual saves over the SAME slot with a save that is
// NOT itself debug-touched this time. Because the prior on-disk save at
// that slot was debug-touched, the merged result must stay
// debugTouched=true (SEC-024's sticky guarantee) — proving the real
// SaveManual save-over path, not just a direct writeBundle unit call,
// reads the prior header and merges it forward.
func TestWriteBundle_SaveOverCarriesDebugTouchedForward(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, nil, "test-corr")

	if err := mgr.SaveManual(debugTouchedFixtureContext(1, 0, true), "slot-b"); err != nil {
		t.Fatalf("SaveManual (initial, debug-touched): %v", err)
	}
	dir := manualDir(root, "slot-b")
	priorHeader, err := serialize.ReadHeader(dir)
	if err != nil {
		t.Fatalf("ReadHeader (prior): %v", err)
	}
	if !priorHeader.DebugTouched() {
		t.Fatal("setup: prior save's header.DebugTouched() = false, want true")
	}

	// Save over the same slot again, this time WITHOUT DebugTouched set
	// on the Context — if writeBundle merged the prior header forward
	// correctly, the promoted result must still read debugTouched=true.
	if err := mgr.SaveManual(debugTouchedFixtureContext(2, 0, false), "slot-b"); err != nil {
		t.Fatalf("SaveManual (save-over, not itself debug-touched): %v", err)
	}

	header, err := serialize.ReadHeader(dir)
	if err != nil {
		t.Fatalf("ReadHeader (after save-over): %v", err)
	}
	if !header.DebugTouched() {
		t.Fatal("header.DebugTouched() = false after a save-over of a debug-touched prior save via the real SaveManual path, want true (BUG-157 regression)")
	}
	if header.CreatedAtTick != 2 {
		t.Fatalf("header.CreatedAtTick = %d after save-over, want 2 (the save-over's own tick, not the prior's)", header.CreatedAtTick)
	}
}

// TestWriteBundle_SaveOverOfCleanSaveStaysClean is BUG-157 regression
// case 3: saving over a slot whose prior on-disk save was NOT
// debug-touched, itself also not debug-touched, must stay
// debugTouched=false — MergeDebugTouched must never contaminate a clean
// save-over with a false positive.
func TestWriteBundle_SaveOverOfCleanSaveStaysClean(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, nil, "test-corr")

	if err := mgr.SaveManual(debugTouchedFixtureContext(1, 0, false), "slot-c"); err != nil {
		t.Fatalf("SaveManual (initial, clean): %v", err)
	}
	dir := manualDir(root, "slot-c")
	priorHeader, err := serialize.ReadHeader(dir)
	if err != nil {
		t.Fatalf("ReadHeader (prior): %v", err)
	}
	if priorHeader.DebugTouched() {
		t.Fatal("setup: prior save's header.DebugTouched() = true, want false")
	}

	if err := mgr.SaveManual(debugTouchedFixtureContext(2, 0, false), "slot-c"); err != nil {
		t.Fatalf("SaveManual (save-over, clean): %v", err)
	}

	header, err := serialize.ReadHeader(dir)
	if err != nil {
		t.Fatalf("ReadHeader (after save-over): %v", err)
	}
	if header.DebugTouched() {
		t.Fatal("header.DebugTouched() = true after a save-over of a clean prior save, want false (false-positive contamination)")
	}
}
