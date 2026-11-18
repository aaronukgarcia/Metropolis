package why

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func fixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// samplePatch decodes the golden fixture through the contract type.
func samplePatch(t *testing.T) Patch {
	t.Helper()
	p, err := decodePatch(fixture(t, "sample.json"))
	if err != nil {
		t.Fatalf("golden sample.json refused: %v", err)
	}
	return p
}

func mustMarshal(t *testing.T, p Patch) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// boundScreen returns a screen with subscription "sub-why" bound.
func boundScreen() *Screen {
	s := New("corr-test")
	s.BindSubscription("sub-why")
	return s
}

func apply(s *Screen, raw json.RawMessage) {
	s.ApplyDelta(protocol.Delta{SubscriptionID: "sub-why", Patch: raw})
}

// frame renders the screen into a w x h buffer and returns its rows.
func frame(s *Screen, w, h int) []string {
	buf := core.NewBuffer(w, h)
	s.Render(buf, core.Rect{X: 0, Y: 0, W: w, H: h})
	return bufferRows(buf)
}

func bufferRows(buf *core.Buffer) []string {
	w, h := buf.Size()
	rows := make([]string, h)
	for y := 0; y < h; y++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			r := buf.Get(x, y).Rune
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
		}
		rows[y] = strings.TrimRight(b.String(), " ")
	}
	return rows
}

// nonEmpty drops trailing blank rows.
func nonEmpty(rows []string) []string {
	n := len(rows)
	for n > 0 && rows[n-1] == "" {
		n--
	}
	return rows[:n]
}

func joined(rows []string) string { return strings.Join(rows, "\n") }
