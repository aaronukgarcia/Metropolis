package compose

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/power"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestViewportView_PowerLines_OmittedWhenEmpty pins FEAT-1972079851's
// omitempty contract: with nothing placed (today's permanent state until
// a later trio slice adds placement callers), the published JSON carries
// NO powerLines key at all — byte-identical to the pre-power schema, so
// consumers see no change until pylons actually exist.
func TestViewportView_PowerLines_OmittedWhenEmpty(t *testing.T) {
	cid := errs.NewCorrelationID()
	e := core.NewEngine()
	comp, err := Wire(e, &Deps{CorrelationID: cid})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	raw, err := comp.state.buildViewportPatch()
	if err != nil {
		t.Fatalf("buildViewportPatch: %v", err)
	}
	var generic map[string]json.RawMessage
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("unmarshalling patch as generic object: %v", err)
	}
	if _, present := generic["powerLines"]; present {
		t.Fatal(`patch carries a "powerLines" key with nothing placed — omitempty is not holding, so every consumer sees a phantom empty layer`)
	}
}

// TestViewportView_PowerLines_CarryPlacedSpans proves the full vertical:
// a span placed through the composed PowerAPI surfaces in the very next
// f1.viewport publish, in placement-ID order, with its catalogue capacity.
func TestViewportView_PowerLines_CarryPlacedSpans(t *testing.T) {
	cid := errs.NewCorrelationID()
	e := core.NewEngine()
	comp, err := Wire(e, &Deps{CorrelationID: cid})
	if err != nil {
		t.Fatalf("Wire: %v", err)
	}
	first, err := comp.state.power.PlaceLine("localPole", 0, 0, 10, 0, cid)
	if err != nil {
		t.Fatalf("PlaceLine localPole: %v", err)
	}
	second, err := comp.state.power.PlaceLine("superGrid", 5, 5, 50, 50, cid)
	if err != nil {
		t.Fatalf("PlaceLine superGrid: %v", err)
	}
	if second.ID <= first.ID {
		t.Fatalf("placement IDs not ascending: %d then %d", first.ID, second.ID)
	}

	raw, err := comp.state.buildViewportPatch()
	if err != nil {
		t.Fatalf("buildViewportPatch: %v", err)
	}
	var patch viewportWirePatch
	if err := json.Unmarshal(raw, &patch); err != nil {
		t.Fatalf("unmarshalling f1.viewport patch: %v", err)
	}
	if len(patch.PowerLines) != 2 {
		t.Fatalf("patch carries %d powerLines, want 2", len(patch.PowerLines))
	}
	want := []viewportPowerLine{
		{ID: first.ID, Class: "localPole", FromX: 0, FromY: 0, ToX: 10, ToY: 0, CapacityMW: 0.5},
		{ID: second.ID, Class: "superGrid", FromX: 5, FromY: 5, ToX: 50, ToY: 50, CapacityMW: 400},
	}
	for i, w := range want {
		if patch.PowerLines[i] != w {
			t.Errorf("powerLines[%d] = %+v, want %+v", i, patch.PowerLines[i], w)
		}
	}
}

// TestWire_FailingPowerLoader_NoComposition asserts AC-4's posture for
// the new dependency: a failing LoadPower leaves no composition behind.
func TestWire_FailingPowerLoader_NoComposition(t *testing.T) {
	cid := errs.NewCorrelationID()
	e := core.NewEngine()
	_, err := Wire(e, &Deps{
		CorrelationID: cid,
		LoadPower: func(string) (*power.PowerAPI, error) {
			return nil, errs.New(power.ErrCatalogueDataInvalid, cid, map[string]any{"cause": "test seam"})
		},
	})
	if err == nil {
		t.Fatal("Wire accepted a failing power loader, want ErrModuleFailed naming \"power\"")
	}
	if !bytes.Contains([]byte(err.Error()), []byte("power")) {
		t.Fatalf("error %v does not name the failing module", err)
	}
}
