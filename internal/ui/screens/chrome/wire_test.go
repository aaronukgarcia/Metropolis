package chrome

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

// TestDecodeFiguresPatch checks the happy path of the "chrome.topbar" wire
// schema (AC-1): all six fields decode field-for-field.
func TestDecodeFiguresPatch(t *testing.T) {
	f, err := decodeFiguresPatch(mustFiguresPatch(t, "Aug 2026", 14, 2, 123456, 50000, "AA"))
	if err != nil {
		t.Fatal(err)
	}
	if f.Date != "Aug 2026" || f.ClockCycle != 14 || f.Speed != 2 || f.Money != 123456 || f.Population != 50000 || f.Rating != "AA" {
		t.Fatalf("decoded figures = %+v", f)
	}
}

// TestDecodeFiguresPatchInvalidJSON checks that invalid JSON is rejected with
// an error, never silently decoded into zero figures.
func TestDecodeFiguresPatchInvalidJSON(t *testing.T) {
	if _, err := decodeFiguresPatch(json.RawMessage("{not json")); err == nil {
		t.Fatal("decodeFiguresPatch(invalid JSON) returned nil error")
	}
}

// TestDecodeFiguresPatchUnsupportedVersion checks that a patch carrying an
// unrecognised schemaVersion is rejected (last-known-good figures stand),
// never guessed at.
func TestDecodeFiguresPatchUnsupportedVersion(t *testing.T) {
	raw, err := json.Marshal(map[string]any{"schemaVersion": 99, "figures": map[string]any{"date": "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeFiguresPatch(raw); err == nil {
		t.Fatal("decodeFiguresPatch(schemaVersion 99) returned nil error")
	}
}

// TestApplyFiguresPatchMalformedKeepsLastKnownGood checks AC-1's safety half:
// a malformed patch is dropped and the top bar keeps its last-known-good
// figures — it never renders a corrupted/partial state (GR#1).
func TestApplyFiguresPatchMalformedKeepsLastKnownGood(t *testing.T) {
	c := NewChrome("test", widgets.DefaultPalette, Effects{})
	c.ApplyFiguresPatch(mustFiguresPatch(t, "Aug 2026", 14, 2, 123456, 50000, "AA"))
	before := c.Figures()

	c.ApplyFiguresPatch(json.RawMessage("{bad"))

	if after := c.Figures(); after != before {
		t.Fatalf("malformed patch changed figures: before=%+v after=%+v", before, after)
	}
}
