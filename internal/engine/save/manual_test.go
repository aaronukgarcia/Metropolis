package save

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/serialize"
)

// TestSaveManual_RoundTripFidelity is AC-3: SaveManual against a fixture
// world with non-trivial state in two different registered participants,
// confirming ValidateBundle passes and loading it back via this
// package's own Load reconstructs the pre-save state field-by-field.
func TestSaveManual_RoundTripFidelity(t *testing.T) {
	root := t.TempDir()
	widgets := newWidgetParticipant(
		widget{ID: 1, Name: "alpha", Score: 3.5},
		widget{ID: 2, Name: "beta", Score: -1.25},
	)
	gadgets := newGadgetParticipant(
		gadget{SerialNo: "SN-001", Weight: 10},
		gadget{SerialNo: "SN-002", Weight: 22},
	)
	mgr := NewManager(root, []Participant{widgets, gadgets}, "test-corr")

	ctx := fixtureContext(100, 12)
	if err := mgr.SaveManual(ctx, "before-port"); err != nil {
		t.Fatalf("SaveManual: %v", err)
	}

	dir := manualDir(root, "before-port")
	if _, err := serialize.ValidateBundle(dir); err != nil {
		t.Fatalf("ValidateBundle on manual save: %v", err)
	}

	preWidgets := widgets.State()
	preGadgets := gadgets.State()

	// Load into FRESH participant instances (a different process would
	// have a fresh registry) so the comparison genuinely proves
	// reconstruction, not just that the originals were never mutated.
	loadWidgets := newWidgetParticipant()
	loadGadgets := newGadgetParticipant()
	loadMgr := NewManager(root, []Participant{loadWidgets, loadGadgets}, "test-corr")

	header, meta, err := loadMgr.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if meta.SaveKind != KindManual {
		t.Fatalf("Load meta.SaveKind = %q, want %q", meta.SaveKind, KindManual)
	}
	if header.CreatedAtTick != ctx.CreatedAtTick {
		t.Fatalf("header.CreatedAtTick = %d, want %d", header.CreatedAtTick, ctx.CreatedAtTick)
	}

	if !reflect.DeepEqual(preWidgets, loadWidgets.State()) {
		t.Fatalf("widget state mismatch after load: pre=%+v post=%+v", preWidgets, loadWidgets.State())
	}
	if !reflect.DeepEqual(preGadgets, loadGadgets.State()) {
		t.Fatalf("gadget state mismatch after load: pre=%+v post=%+v", preGadgets, loadGadgets.State())
	}
}

// TestSaveManual_NeverPruned is a sanity check that manual saves live
// under a distinct subtree Autosave retention never touches.
func TestSaveManual_NeverPruned(t *testing.T) {
	root := t.TempDir()
	mgr := NewManager(root, nil, "test-corr")
	for i := 0; i < 3; i++ {
		if err := mgr.SaveManual(fixtureContext(int64(i), 0), fmt.Sprintf("save-%d", i)); err != nil {
			t.Fatalf("SaveManual %d: %v", i, err)
		}
	}
	summaries, readErrs, err := List(root)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(readErrs) != 0 {
		t.Fatalf("List readErrs = %v, want none", readErrs)
	}
	if len(summaries) != 3 {
		t.Fatalf("List returned %d summaries, want 3 manual saves all retained", len(summaries))
	}
}
