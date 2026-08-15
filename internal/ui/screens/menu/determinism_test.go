package menu

// SF-8 (GR#21 determinism): rendering is a pure function of (view-model
// state, navigation/selection state) — identical inputs render identically
// across repeated calls; no time.Now()-driven content beyond the shared
// threshold-pulse primitive (unused by this package). None of this
// package's production code calls the wall clock directly.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// TestNoWallClockUsage mechanically encodes SF-8's own grep check
// ("grep -rn time.Now internal/ui/screens/menu/*.go, excluding _test.go,
// returns no matches") as a real test, mirroring ui.screen.demo's
// TestNoWallClockUsage.
func TestNoWallClockUsage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	needle := []byte("time.Now(")
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || filepath.Ext(name) != ".go" {
			continue
		}
		if len(name) >= len("_test.go") && name[len(name)-len("_test.go"):] == "_test.go" {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("ReadFile(%s): %v", name, err)
		}
		if bytes.Contains(b, needle) {
			t.Errorf("%s calls time.Now() directly -- this package must never read the wall clock (SF-8/GR#21)", name)
		}
	}
}

// TestRender_IdenticalInputsRenderIdentically is SF-8's positive check:
// calling every Render* function twice with the same inputs produces
// byte-identical buffers.
func TestRender_IdenticalInputsRenderIdentically(t *testing.T) {
	entries := []SaveEntry{
		{Name: "newtown", Path: "/s/newtown", CreatedAtTick: 400, GameMonth: 5, WorldSeed: 1001, Summary: "seed 1001"},
		{Name: "oldtown", Path: "/s/oldtown", CreatedAtTick: 120, GameMonth: 2, WorldSeed: 1002, Summary: "seed 1002 · debug"},
	}
	session := Session{WorldSeed: 1001, Tick: 400, GameMonth: 5, Paused: false, Speed: 1}
	req := NewGameRequest{Seed: "12345", Debug: true}
	schema := []SettingSpec{
		{Key: "audio.enabled", Label: "Audio", Kind: SettingBool, Default: "true"},
		{Key: "video.mode", Label: "Video mode", Kind: SettingChoice, Choices: []string{"low", "high"}, Default: "high"},
	}
	values := map[string]string{"audio.enabled": "true", "video.mode": "low"}
	rect := core.Rect{X: 0, Y: 0, W: 60, H: 6}

	render := func() *core.Buffer {
		buf := core.NewBuffer(60, 6)
		RenderSaves(buf, rect, entries, tcell.StyleDefault)
		RenderSession(buf, rect, session, tcell.StyleDefault)
		RenderNewGameForm(buf, rect, req, tcell.StyleDefault)
		RenderSettings(buf, rect, schema, values, tcell.StyleDefault)
		return buf
	}

	a := render()
	b := render()
	w, h := a.Size()
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if a.Get(x, y) != b.Get(x, y) {
				t.Fatalf("render not deterministic at (%d,%d): %+v vs %+v", x, y, a.Get(x, y), b.Get(x, y))
			}
		}
	}
}
