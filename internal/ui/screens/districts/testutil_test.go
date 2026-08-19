package districts

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// mustJSON marshals v to a json.RawMessage, failing the test on error --
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
// as spaces), mirroring ui.screen.services' renderedText helper.
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

// rowContains reports whether any rendered row contains sub -- a loose
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

// fullPatch returns a wirePatch populating TaxSettings with deterministic
// fixture data (schemaVersion 1) -- the shared baseline the screen/
// regression tests apply. Districts is deliberately never populated here
// (AC-2 is BLOCKED, see doc.go) -- a fixture that sent it would
// misrepresent what any real engine sends today.
func fullPatch() wirePatch {
	taxSettings := []wireDistrictTaxSetting{
		{DistrictID: "harbour", InstrumentID: "councilTax", InstrumentLabel: "Council Tax", Multiplier: 1.5, Rate: 10, RateMax: 20, EffectiveRate: 15},
		{DistrictID: "harbour", InstrumentID: "businessRates", InstrumentLabel: "Business Rates", Multiplier: 1.0, Rate: 8, RateMax: 16, EffectiveRate: 8},
		{DistrictID: "old-town", InstrumentID: "councilTax", InstrumentLabel: "Council Tax", Multiplier: 0.8, Rate: 10, RateMax: 20, EffectiveRate: 8},
	}
	return wirePatch{
		SchemaVersion: 1,
		TaxSettings:   &taxSettings,
	}
}
