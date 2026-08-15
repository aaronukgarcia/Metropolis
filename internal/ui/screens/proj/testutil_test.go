package proj

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
// as spaces), mirroring ui.screen.demo's renderedText helper — the
// test-side assertion surface for text rows (labels, status lines).
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

// isBraille reports whether r is a Braille-pattern codepoint (U+2800..
// U+28FF). Used to distinguish a real chart (Braille dots) from a bare
// text/number rendering.
func isBraille(r rune) bool {
	return r >= brailleBase && r < brailleBase+0x100
}
