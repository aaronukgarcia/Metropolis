package freight_test

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/freight"
	"github.com/aaronukgarcia/Metropolis/internal/engine/rail"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/data"
)

// TestIntermodalTonnesConservation is AC-4's false-pass guard, run end-to-end
// against the real engine.rail stub: a known tonnage moved through the
// intermodal transfer point must conserve tonnes, with in and out summed
// INDEPENDENTLY from the account (never a conservation-OK flag). No sea leg
// appears here: sea's 3,000 t minimum exceeds road's 25 t and rail's 1,000 t
// per-movement maxes, so a conserving sea↔rail handoff is unrepresentable
// (SEC-125) — the rail↔road pair is the valid surface.
func TestIntermodalTonnesConservation(t *testing.T) {
	dir, err := data.ResolveDataDir("cp-rail-test")
	if err != nil {
		t.Fatalf("ResolveDataDir: %v", err)
	}
	f, err := freight.Load(dir, "cp-rail-test")
	if err != nil {
		t.Fatalf("freight.Load: %v", err)
	}
	cp, err := freight.LoadContainerPort(dir, "cp-rail-test", f)
	if err != nil {
		t.Fatalf("freight.LoadContainerPort: %v", err)
	}

	r, err := rail.NewRailAPI("cp-rail-test")
	if err != nil {
		t.Fatalf("rail.NewRailAPI: %v", err)
	}
	cp.WireRail(r)

	if _, err := cp.IntermodalTransfer(freight.ModeRail, freight.ModeRoad, 25); err != nil {
		t.Fatalf("rail→road: %v", err)
	}
	if _, err := cp.IntermodalTransfer(freight.ModeRoad, freight.ModeRail, 25); err != nil {
		t.Fatalf("road→rail: %v", err)
	}
	if _, err := cp.IntermodalTransfer(freight.ModeRail, freight.ModeRoad, 25); err != nil {
		t.Fatalf("rail→road: %v", err)
	}

	acct, err := cp.IntermodalAccount()
	if err != nil {
		t.Fatalf("IntermodalAccount: %v", err)
	}
	var inTotal, outTotal, dwellTotal int64
	for _, v := range acct.InTonnes {
		inTotal += v
	}
	for _, v := range acct.OutTonnes {
		outTotal += v
	}
	for _, v := range acct.DwellTonnes {
		dwellTotal += v
	}
	if inTotal != outTotal+dwellTotal {
		t.Fatalf("conservation violated: in %d != out %d + dwell %d", inTotal, outTotal, dwellTotal)
	}
	if inTotal != 75 {
		t.Fatalf("expected 75 tonnes through the transfer point, got in %d", inTotal)
	}
}
