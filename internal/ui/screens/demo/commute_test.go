package demo

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// TestRenderCommuteLeak_InOutCommutingDistinct is DEMO-6's check: the
// two commuting directions render as distinct figures, not merged into
// one undifferentiated "commuting" number.
func TestRenderCommuteLeak_InOutCommutingDistinct(t *testing.T) {
	rect := core.Rect{X: 0, Y: 0, W: 60, H: 2}
	buf := core.NewBuffer(60, 2)
	figures := CommuteFigures{OutCommuters: 4321, InCommuters: 1234}
	RenderCommuteLeak(buf, rect, figures, tcell.StyleDefault)
	lines := renderedText(buf, rect)

	if len(lines) != 2 {
		t.Fatalf("len(lines) = %d, want 2", len(lines))
	}
	if !strings.Contains(lines[0], "Out-commuting") || !strings.Contains(lines[0], "4321") {
		t.Errorf("line 0 = %q, want it to name Out-commuting and show 4321", lines[0])
	}
	if !strings.Contains(lines[1], "In-commuting") || !strings.Contains(lines[1], "1234") {
		t.Errorf("line 1 = %q, want it to name In-commuting and show 1234", lines[1])
	}
	if lines[0] == lines[1] {
		t.Fatalf("out/in commuting lines are identical -- directions must be distinguished, not merged")
	}
}

// TestCommute_SF3_OneDirectionChanges proves the two directions are
// wired to genuinely independent fields (SF-3 shape): mutating only
// OutCommuters must change only the out-commuting rendered line.
func TestCommute_SF3_OneDirectionChanges(t *testing.T) {
	rect := core.Rect{X: 0, Y: 0, W: 60, H: 2}

	bufA := core.NewBuffer(60, 2)
	RenderCommuteLeak(bufA, rect, CommuteFigures{OutCommuters: 100, InCommuters: 50}, tcell.StyleDefault)
	linesA := renderedText(bufA, rect)

	bufB := core.NewBuffer(60, 2)
	RenderCommuteLeak(bufB, rect, CommuteFigures{OutCommuters: 900, InCommuters: 50}, tcell.StyleDefault)
	linesB := renderedText(bufB, rect)

	if linesA[0] == linesB[0] {
		t.Errorf("out-commuting line unchanged after mutating OutCommuters 100 -> 900")
	}
	if linesA[1] != linesB[1] {
		t.Errorf("in-commuting line changed even though InCommuters was untouched: %q -> %q", linesA[1], linesB[1])
	}
}

// TestApplyCommute_DecodesBothDirections drives the wire patch through
// Screen.applyCommute and confirms both figures are wired to the real
// f6.commute schema fields, not a hardcoded stand-in.
func TestApplyCommute_DecodesBothDirections(t *testing.T) {
	s := New("corr-commute")
	s.applyCommute(mustJSON(t, wireCommutePatch{SchemaVersion: 1, OutCommuters: 777, InCommuters: 333}))
	got, have := s.Commute()
	if !have {
		t.Fatalf("Commute() haveCommute = false after a valid patch")
	}
	if got.OutCommuters != 777 || got.InCommuters != 333 {
		t.Fatalf("Commute() = %+v, want {OutCommuters:777 InCommuters:333}", got)
	}
}
