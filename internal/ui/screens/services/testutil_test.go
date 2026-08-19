package services

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
// as spaces), mirroring ui.screen.trade's renderedText helper.
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
// screen/regression tests apply. PublicServicePie is deliberately never
// populated here (SVC-6 is BLOCKED, see doc.go) — a fixture that sent it
// would misrepresent what the engine actually sends today.
func fullPatch() wirePatch {
	sliders := []wireServiceSlider{
		{ID: "police", Label: "Police", Value: 500, Min: 0, Max: 1000, Step: 10},
		{ID: "fire", Label: "Fire", Value: 300, Min: 0, Max: 800, Step: 10},
	}
	capacityDemand := []wireCapacityDemand{
		{ServiceID: "police", Label: "Police", CapacityUnits: 100, DemandUnits: 80},
		{ServiceID: "fire", Label: "Fire", CapacityUnits: 60, DemandUnits: 45},
	}
	responseTimes := []wireResponseTimeStat{
		{ServiceID: "fire", Label: "Fire", MedianSeconds: 240, P90Seconds: 480, SampleCount: 120},
		{ServiceID: "ambulance", Label: "Ambulance", MedianSeconds: 420, P90Seconds: 900, SampleCount: 96},
	}
	waitingLists := []wireWaitingList{
		{ID: "hospital-nonurgent", Label: "Hospital (non-urgent)", CurrentCount: 340, TrendHistory: []float64{300, 310, 305, 320, 330, 325, 335, 338, 340, 342, 339, 340}},
	}
	return wirePatch{
		SchemaVersion:  1,
		Sliders:        &sliders,
		CapacityDemand: &capacityDemand,
		ResponseTimes:  &responseTimes,
		WaitingLists:   &waitingLists,
	}
}
