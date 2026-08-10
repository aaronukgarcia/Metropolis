package uitest

import (
	"encoding/json"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/harness/replay"
	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/gdamore/tcell/v2"

	uicore "github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// testSubID is the SubscriptionID every test fixture built by
// buildFixture uses.
const testSubID protocol.SubscriptionID = "uitest-sub"

// buildFixture writes and reloads a harness.replay fixture (round-tripped
// through Save/Load exactly like a real recording, not an in-memory-only
// Recorder) carrying n Deltas for testSubID, Seq/Tick 1..n, each with a
// {"n": <seq>} patch — enough content for tests to draw from without
// depending on any engine module's real patch schema.
func buildFixture(t *testing.T, n int) replay.Fixture {
	t.Helper()
	dir := t.TempDir()
	rec := replay.NewRecorder()
	for i := 1; i <= n; i++ {
		patch, err := json.Marshal(map[string]int{"n": i})
		if err != nil {
			t.Fatalf("marshal patch: %v", err)
		}
		d := protocol.Delta{SubscriptionID: testSubID, Tick: protocol.Tick(i), Seq: uint64(i), Patch: patch}
		if err := rec.ObserveDelta(d); err != nil {
			t.Fatalf("ObserveDelta: %v", err)
		}
	}
	if err := replay.Save(dir, "uitest-fixture", rec, replay.FixtureMeta{WorldSeed: 1, AppVersion: "uitest-test"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	fx, err := replay.Load(dir, "uitest-fixture")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return fx
}

// truncateFixture returns a fixture carrying only fx's first keep
// Records — AC-3b's "deliberately truncated" scenario, derived from a
// real (if synthetic) recorded stream rather than hand-built to look
// truncated.
func truncateFixture(fx replay.Fixture, keep int) replay.Fixture {
	return replay.Fixture{Header: fx.Header, Records: fx.Records[:keep]}
}

// countDraw is a uicore.DrawFunc shared by several tests: it decodes
// testSubID's current patch's "n" field and writes '0'+(n%10) into cell
// (0,0), plus 'S' at (1,0) if the subscription is currently stale, '.'
// otherwise — deterministic and cheap, enough to prove Capture()/
// AssertSnapshot see real ViewModels content an attached fixture drove.
func countDraw(back *uicore.Buffer, vm *uicore.ViewModels) {
	var payload struct {
		N int `json:"n"`
	}
	if raw, ok := vm.Patches[testSubID]; ok {
		_ = json.Unmarshal(raw, &payload)
	}
	back.Set(0, 0, rune('0'+(payload.N%10)), tcell.StyleDefault)
	if vm.Stale[testSubID] {
		back.Set(1, 0, 'S', tcell.StyleDefault)
	} else {
		back.Set(1, 0, '.', tcell.StyleDefault)
	}
}
