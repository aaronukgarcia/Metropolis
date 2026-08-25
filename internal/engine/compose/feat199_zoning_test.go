package compose

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/core"
	"github.com/aaronukgarcia/Metropolis/internal/engine/world"
	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// FEAT-199's compose-side proof set: a KindZone command carrying a
// density (a) validates against data/zoning.json's per-family ladder,
// (b) writes the family + level into engine.world's per-cell ledger via
// ApplyOwnershipCommand, and (c) publishes zone/zoneDensity/zoneColourKey
// on the f1.viewport patch. The negative case proves an out-of-ladder
// density rejects through the registry (MET-E407) and mutates nothing.

func feat199ZoneCmd(t *testing.T, e *core.Engine, corr string, density int) protocol.CommandResult {
	t.Helper()
	buy := protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(corr + "-buy"),
		Kind:            protocol.KindBuy,
		Payload:         protocol.BuyPayload{Cell: protocol.CellRef{X: 0, Y: 0}},
	}
	if res := e.HandleCommand(buy); !res.Accepted {
		t.Fatalf("Buy rejected: %+v", res.Error)
	}
	return e.HandleCommand(protocol.Command{
		ProtocolVersion: protocol.ProtocolVersion,
		CorrelationID:   protocol.CorrelationID(corr),
		Kind:            protocol.KindZone,
		Payload:         protocol.ZonePayload{Cell: protocol.CellRef{X: 0, Y: 0}, ZoneType: "dwelling", Density: density},
	})
}

// TestZoneDensityWriteThroughToWorldLedger is the loop-runs proof:
// zoning "dwelling" at density 4 leaves Cell.Zoning=Residential and
// Cell.ZoningDensity=4 in engine.world — real state, not just build's own
// zoneState map.
func TestZoneDensityWriteThroughToWorldLedger(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	if res := feat199ZoneCmd(t, e, "feat199-zone4", 4); !res.Accepted {
		t.Fatalf("Zone(density=4) rejected: %+v", res.Error)
	}

	st := comp.state
	cell, err := st.world.CellAt(world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY}, world.CellLocal{Row: 0, Col: 0}, st.cid)
	if err != nil {
		t.Fatalf("CellAt: %v", err)
	}
	if cell.Zoning != world.ZoningResidential || cell.ZoningDensity != 4 {
		t.Fatalf("world cell = zoning %v density %d, want Residential/4 — the write-through did not land", cell.Zoning, cell.ZoningDensity)
	}
}

// TestViewportPatchCarriesZoningFields proves the f1.viewport publish
// surfaces the zoned cell's family id, density level and semantic palette
// key derived from the catalogue ("dwelling"+4 -> residential/4/"res4").
func TestViewportPatchCarriesZoningFields(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	if res := feat199ZoneCmd(t, e, "feat199-viewport", 4); !res.Accepted {
		t.Fatalf("Zone(density=4) rejected: %+v", res.Error)
	}

	raw, err := comp.state.buildViewportPatch()
	if err != nil {
		t.Fatalf("buildViewportPatch: %v", err)
	}
	var decoded struct {
		Cells []struct {
			X             int    `json:"x"`
			Y             int    `json:"y"`
			Zone          string `json:"zone"`
			ZoneDensity   int    `json:"zoneDensity"`
			ZoneColourKey string `json:"zoneColourKey"`
		} `json:"cells"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode viewport patch: %v", err)
	}

	for _, c := range decoded.Cells {
		if c.X == 0 && c.Y == 0 {
			if c.Zone != "residential" || c.ZoneDensity != 4 || c.ZoneColourKey != "res4" {
				t.Fatalf("viewport cell (0,0) = zone %q density %d key %q, want residential/4/res4", c.Zone, c.ZoneDensity, c.ZoneColourKey)
			}
			return
		}
	}
	t.Fatal("viewport patch has no (0,0) cell")
}

// TestZoneDensityOutOfRangeRejectedNoMutation: density 9 is outside every
// family's ladder; the command rejects with engine.world's MET-E407 code
// and the world ledger stays zero.
func TestZoneDensityOutOfRangeRejectedNoMutation(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	res := feat199ZoneCmd(t, e, "feat199-reject", 9)
	if res.Accepted {
		t.Fatal("Zone(density=9) accepted, want MET-E407 rejection")
	}
	if res.Error == nil || res.Error.Code != world.ErrZoningDensityOutOfRange {
		t.Fatalf("rejection code = %+v, want %s", res.Error, world.ErrZoningDensityOutOfRange)
	}

	st := comp.state
	cell, err := st.world.CellAt(world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY}, world.CellLocal{Row: 0, Col: 0}, errs.NewCorrelationID())
	if err != nil {
		t.Fatalf("CellAt: %v", err)
	}
	if cell.Zoning != world.ZoningNone || cell.ZoningDensity != 0 {
		t.Fatalf("world cell after rejected command = zoning %v density %d, want None/0 — a rejected command must mutate no zone state", cell.Zoning, cell.ZoningDensity)
	}
}

// TestZoneDensityZeroStillWritesFamily: omitting density (wire default 0)
// keeps today's behaviour AND records the family mapping in the ledger at
// level 0 — old senders stay valid under the additive schema.
func TestZoneDensityZeroStillWritesFamily(t *testing.T) {
	e, comp := newTestEngine(t, 42)
	if res := feat199ZoneCmd(t, e, "feat199-zero", 0); !res.Accepted {
		t.Fatalf("Zone(density=0) rejected: %+v", res.Error)
	}

	st := comp.state
	cell, err := st.world.CellAt(world.TileCoord{X: defaultStartCoordX, Y: defaultStartCoordY}, world.CellLocal{Row: 0, Col: 0}, st.cid)
	if err != nil {
		t.Fatalf("CellAt: %v", err)
	}
	if cell.Zoning != world.ZoningResidential || cell.ZoningDensity != 0 {
		t.Fatalf("world cell = zoning %v density %d, want Residential/0", cell.Zoning, cell.ZoningDensity)
	}
}
