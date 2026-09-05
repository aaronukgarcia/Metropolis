package finance

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/gdamore/tcell/v2"
)

// attack_bug769_render_test.go — opus-round-bug769. The post-game-over
// state finance actually reaches (gameOver LATCHES true while
// insolvencyMonths resets to 0 on the next met month — proven live in
// compose's attack_bug769_round_test.go) renders NOTHING, because
// RenderInsolvency short-circuits on Months <= 0 BEFORE it looks at
// Insolvent. The GAME OVER line silently disappears the month after it
// appears.
func TestAttackBUG769_RenderInsolvency_LatchedGameOverWithZeroMonths(t *testing.T) {
	buf := core.NewBuffer(90, 3)
	rect := core.Rect{X: 0, Y: 0, W: 90, H: 3}
	RenderInsolvency(buf, rect, InsolvencyView{Months: 0, Insolvent: true, Verdict: "insolvency"}, true, tcell.StyleDefault)
	drawn := false
	for x := 0; x < 90; x++ {
		if buf.Get(x, 0).Rune == 'I' {
			drawn = true
			break
		}
	}
	if !drawn {
		t.Errorf("FINDING: RenderInsolvency drew NOTHING for Months=0 + Insolvent=true + Verdict=insolvency — the real, reachable post-latch state (finance.gameOver never clears, insolvencyMonths resets on the next met month). The GAME OVER line vanishes.")
	}
}
