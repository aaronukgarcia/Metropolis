package trade

import (
	"encoding/json"
	"strings"
	"testing"

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
// as spaces), mirroring ui.screen.proj's renderedText helper — the
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
// screen/regression tests apply.
func fullPatch() wirePatch {
	contracts := []wireContract{
		{ID: "c-1", Commodity: "grain", TermMonths: 12, MonthsRemaining: 8, CancellationPenaltyMicropounds: 1500000, PricePerUnitMicropounds: 45_000_000, Status: "active"},
		{ID: "c-2", Commodity: "fuel", TermMonths: 6, MonthsRemaining: 2, CancellationPenaltyMicropounds: 0, PricePerUnitMicropounds: 90_000_000, Status: "active"},
	}
	junctions := []wireJunction{
		{JunctionID: "junction:14", Label: "M20/A20 gyratory", Approaches: []wireApproach{
			{ApproachID: "north", Cargo: "freight", TruckCount: 12, WaitSeconds: 45},
			{ApproachID: "south", Cargo: "fuel", TruckCount: 3, WaitSeconds: 8},
		}},
	}
	warehouse := []wireWarehouse{
		{Commodity: "grain", StockTonnes: 1200, CapacityTonnes: 2000, BufferTonnesPerDay: 25, FlowTonnesPerDay: 18},
		{Commodity: "fuel", StockTonnes: 400, CapacityTonnes: 600, BufferTonnesPerDay: 10, FlowTonnesPerDay: 12},
	}
	port := wirePort{Unlocked: true, Berths: 4, CraneRateTonnesPerHour: 40, OperatingHoursPerDay: 16, CustomsThroughputTonnesPerDay: 1500, SmugglingRisk: 0.35}
	balance := wireBalance{
		Imports: &wireLedger{
			ByCommodity: []wireTradeFlow{{Key: "grain", TonnesPerDay: 40, ValuePerDayMicropounds: 1800000}},
			ByArtery:    []wireTradeFlow{{Key: "sea", TonnesPerDay: 60, ValuePerDayMicropounds: 2700000}},
		},
		Exports: &wireLedger{
			ByCommodity: []wireTradeFlow{{Key: "machinery", TonnesPerDay: 12, ValuePerDayMicropounds: 9600000}},
			ByArtery:    []wireTradeFlow{{Key: "rail", TonnesPerDay: 12, ValuePerDayMicropounds: 9600000}},
		},
	}
	safety := wireSafety{Corridors: []wireCorridor{
		{Corridor: "port-refinery", PipelineCapacityTonnesPerDay: 500, TruckMovementsPerDay: 120, LeakRisk: 0.02},
	}}
	return wirePatch{
		SchemaVersion: 1,
		Contracts:     &contracts,
		Junctions:     &junctions,
		Warehouse:     &warehouse,
		Port:          &port,
		Balance:       &balance,
		Safety:        &safety,
	}
}
