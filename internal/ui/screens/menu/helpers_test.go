package menu

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// mustJSON marshals v to a json.RawMessage, failing the test on error
// (mirrors ui.screen.demo's mustJSON helper).
func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return b
}

// renderedText flattens a buffer's rect into trimmed line strings, for
// asserting rendered output (mirrors ui.screen.demo's renderedText).
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
