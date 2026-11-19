package chrome

// FEAT-9995385625: the top line shows the treasury as whole pounds with
// thousands commas, whatever its size, and never cuts it at the game's
// minimum width.

import (
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func TestFEAT9995385625_TopLineMoneyStyle(t *testing.T) {
	cases := []struct {
		money int64
		want  string
	}{
		{26851123, "money £26,851,123 |"},
		{123485323, "money £123,485,323 |"},
		{123456789012, "money £123,456,789,012 |"}, // twelve digits
		{0, "money £0 |"},
		{-1234, "money -£1,234 |"},
	}
	for _, c := range cases {
		ch := NewChrome("test", widgets.DefaultPalette, Effects{})
		ch.ApplyFiguresPatch(mustFiguresPatch(t, "Aug 2026", 14, 2, c.money, 50000, "AA"))
		buf := core.NewBuffer(120, 6)
		ch.Render(buf, core.Rect{X: 0, Y: 0, W: 120, H: 6})
		top := rowString(buf, 0, 120)
		if !strings.Contains(top, c.want) {
			t.Errorf("money %d: top line %q does not contain %q", c.money, top, c.want)
		}
		if strings.Contains(top, ".") {
			t.Errorf("money %d: old style on the top line %q", c.money, top)
		}
		// Not cut: the rating, last on the line, is still there.
		if !strings.HasSuffix(strings.TrimRight(top, " "), "rating AA") {
			t.Errorf("money %d: top line cut before the rating: %q", c.money, top)
		}
	}
}
