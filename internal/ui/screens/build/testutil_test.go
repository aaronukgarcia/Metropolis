package build

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// mustJSON marshals v to a json.RawMessage, failing the test on error —
// the wire structs are fixed and known-marshalable, so a failure here
// indicates a real bug, not an expected condition.
func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

// renderedText returns each row of rect as a trimmed string (blank runes
// as spaces), mirroring ui.screen.trade's renderedText helper — the
// test-side assertion surface for text rows (labels, status lines, table
// rows).
func renderedText(buf *core.Buffer, rect core.Rect) []string {
	var lines []string
	for y := rect.Y; y < rect.Y+rect.H; y++ {
		var sb strings.Builder
		for x := rect.X; x < rect.X+rect.W; x++ {
			c := buf.Get(x, y)
			if c.Rune == 0 {
				sb.WriteByte(' ')
			} else {
				sb.WriteRune(c.Rune)
			}
		}
		lines = append(lines, strings.TrimRight(sb.String(), " "))
	}
	return lines
}

// rowContains reports whether any rendered row contains sub — a loose
// assertion for "this figure appears in the rendered output" checks that
// is not sensitive to exact column layout.
func rowContains(rows []string, sub string) bool {
	for _, r := range rows {
		if strings.Contains(r, sub) {
			return true
		}
	}
	return false
}

// fullPatch returns a wirePatch populating every sub-surface with
// deterministic fixture data (schemaVersion 1) — the shared baseline the
// screen/regression tests apply. The eight §34 zone slugs match
// data/buildings.json's zone catalogue exactly (they are the engine's
// stable keys, carried verbatim on the view).
func fullPatch() wirePatch {
	zones := []wireZone{
		{ID: "dwelling", Name: "Dwelling", Materials: 100, Labour: 40, BaseLeadTimeDays: 45},
		{ID: "shop", Name: "Shop", Materials: 80, Labour: 30, BaseLeadTimeDays: 30},
		{ID: "office", Name: "Office", Materials: 150, Labour: 50, BaseLeadTimeDays: 60},
		{ID: "entertainment", Name: "Entertainment", Materials: 200, Labour: 60, BaseLeadTimeDays: 75},
		{ID: "farming", Name: "Farming", Materials: 60, Labour: 20, BaseLeadTimeDays: 20},
		{ID: "manufacturing", Name: "Manufacturing", Materials: 250, Labour: 80, BaseLeadTimeDays: 90},
		{ID: "heavy_industry", Name: "Heavy Industry", Materials: 400, Labour: 120, BaseLeadTimeDays: 150},
		{ID: "mining", Name: "Mining", Materials: 300, Labour: 100, BaseLeadTimeDays: 120},
	}
	queue := []wireBuildOrder{
		{ID: 1, Cell: protocol.CellRef{X: 2, Y: 3}, Zone: "dwelling", MaterialsBillTotal: 100, MaterialsDrawn: 40, MaterialsRemaining: 60, LabourRemaining: 20, LeadTimeRemaining: 15, Status: "in-progress"},
		{ID: 2, Cell: protocol.CellRef{X: 5, Y: 7}, Zone: "manufacturing", MaterialsBillTotal: 250, MaterialsDrawn: 0, MaterialsRemaining: 250, LabourRemaining: 80, LeadTimeRemaining: 90, Status: "materials-pending"},
	}
	catalogue := []wireCatalogueEntry{
		{ID: "footpath", Name: "Footpath", Section: "R", CostRaw: "20k", CapacityRaw: "", Notes: "walk/cycle only", UnlockState: "unlocked"},
		{ID: "motorway_extension", Name: "Motorway extension", Section: "R", CostRaw: "6M", CapacityRaw: "", Notes: "ties into M20", UnlockState: "locked"},
		{ID: "avenue_2_2_parking", Name: "Avenue (2+2, parking)", Section: "R", CostRaw: "900k", CapacityRaw: "", Notes: "", UnlockState: "in-progress"},
	}
	landPrice := wireLandPrice{Cell: protocol.CellRef{X: 2, Y: 3}, PriceMicropounds: 1_250_000}
	demolition := wireDemolition{Cell: protocol.CellRef{X: 2, Y: 3}, CompensationMicropounds: 600_000}
	return wirePatch{
		SchemaVersion: 1,
		Zones:         &zones,
		Queue:         &queue,
		Catalogue:     &catalogue,
		LandPrice:     &landPrice,
		Demolition:    &demolition,
	}
}
