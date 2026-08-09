package widgets

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func TestDeriveDelta(t *testing.T) {
	cases := []struct {
		prev, curr float64
		want       Delta
	}{
		{5, 10, DeltaUp},
		{10, 5, DeltaDown},
		{5, 5, DeltaFlat},
	}
	for _, c := range cases {
		if got := DeriveDelta(c.prev, c.curr); got != c.want {
			t.Errorf("DeriveDelta(%v,%v) = %v, want %v", c.prev, c.curr, got, c.want)
		}
	}
}

func TestThresholds_State(t *testing.T) {
	th := Thresholds{Warning: 50, Danger: 80, HigherIsBad: true}
	cases := []struct {
		v    float64
		want ThresholdState
	}{
		{10, StateOK},
		{60, StateWarning},
		{90, StateDanger},
		{80, StateDanger},
		{50, StateWarning},
	}
	for _, c := range cases {
		if got := th.State(c.v); got != c.want {
			t.Errorf("State(%v) = %v, want %v", c.v, got, c.want)
		}
	}
}

func TestBigNum_RendersLabelValueArrowAndSparkline(t *testing.T) {
	buf := core.NewBuffer(12, 3)
	state := BigNumState{
		Label:      "Cash",
		ValueText:  "120k",
		Prev:       100,
		Curr:       120,
		Series:     nil,
		Thresholds: Thresholds{Warning: 200, Danger: 300, HigherIsBad: true},
	}
	BigNum(buf, core.Rect{X: 0, Y: 0, W: 12, H: 3}, state, DefaultPalette, tcell.StyleDefault)

	rows := gridRunes(buf)
	if rows[0] != "Cash        " {
		t.Fatalf("row0 = %q, want %q", rows[0], "Cash        ")
	}
	wantRow1 := "120k " + string(deltaArrow[DeltaUp]) + "      "
	if rows[1] != wantRow1 {
		t.Fatalf("row1 = %q, want %q", rows[1], wantRow1)
	}
	if rows[2] != "            " {
		t.Fatalf("row2 (empty-series sparkline) = %q, want all-blank", rows[2])
	}
}

func TestBigNum_ThresholdColoursValueRow(t *testing.T) {
	buf := core.NewBuffer(12, 2)
	state := BigNumState{
		Label:      "Occupancy",
		ValueText:  "90%",
		Prev:       80,
		Curr:       90,
		Thresholds: Thresholds{Warning: 50, Danger: 80, HigherIsBad: true},
	}
	BigNum(buf, core.Rect{X: 0, Y: 0, W: 12, H: 2}, state, DefaultPalette, tcell.StyleDefault)

	want := DefaultPalette.ThresholdStyle(StateDanger, tcell.StyleDefault)
	if got := buf.Get(0, 1).Style; got != want {
		t.Fatalf("value row style = %v, want danger threshold style %v", got, want)
	}
}

func TestBigNum_DegenerateDoesNotPanic(t *testing.T) {
	buf := core.NewBuffer(4, 4)
	BigNum(buf, core.Rect{X: 0, Y: 0, W: 0, H: 0}, BigNumState{}, DefaultPalette, tcell.StyleDefault)
	BigNum(nil, core.Rect{X: 0, Y: 0, W: 4, H: 4}, BigNumState{}, DefaultPalette, tcell.StyleDefault)
	BigNum(buf, core.Rect{X: 0, Y: 0, W: 4, H: 1}, BigNumState{}, DefaultPalette, tcell.StyleDefault)
}
