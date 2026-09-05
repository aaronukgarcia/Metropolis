package main

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	financescreen "github.com/aaronukgarcia/Metropolis/internal/ui/screens/finance"
)

// TestFinanceDrawFunc_UnlimitedMoneyIndicatorWired and its Real-mode
// control below are FEAT-143's round finding P2-C verification:
// financescreen.RenderMoneyMode was built (AC-7) but financeDrawFunc
// (boot.go) never called it -- a built-but-not-wired gap (the dominant
// defect class in this codebase). These prove the COMPOSED draw closure,
// not just the standalone Render* functions in isolation, surfaces the
// Unlimited Money indicator when the screen's own published patch says
// unlimited=true, and never shows it (leaving the ordinary P&L surface as
// the only display) when Real mode is published instead.

// bufferRowString renders one buffer row as a plain string (0 runes,
// meaning never-written, rendered as a space) for substring assertions --
// far less brittle than matching a single rune, which several other
// section headings on this screen also happen to contain (e.g. "TAX
// INSTRUMENTS & ELASTICITY" contains a 'U').
func bufferRowString(buf *core.Buffer, w, y int) string {
	row := make([]rune, w)
	for x := 0; x < w; x++ {
		r := buf.Get(x, y).Rune
		if r == 0 {
			r = ' '
		}
		row[x] = r
	}
	return string(row)
}

func bufferContainsString(buf *core.Buffer, w, h int, needle string) bool {
	for y := 0; y < h; y++ {
		row := bufferRowString(buf, w, y)
		for x := 0; x+len(needle) <= len(row); x++ {
			if row[x:x+len(needle)] == needle {
				return true
			}
		}
	}
	return false
}

func TestFinanceDrawFunc_UnlimitedMoneyIndicatorWired(t *testing.T) {
	const w, h = 100, 40

	fs := financescreen.New("t-financedraw-unlimited")
	fs.BindSubscription("sub-1")
	fs.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1, "unlimitedMoney": true}`),
	})

	buf := core.NewBuffer(w, h)
	draw := financeDrawFunc(fs)
	draw(buf, nil)

	if !bufferContainsString(buf, w, h, "UNLIMITED MONEY") {
		t.Fatalf("financeDrawFunc composed output has no \"UNLIMITED MONEY\" indicator anywhere -- want RenderMoneyMode's AC-7 banner to have been drawn when the screen published unlimitedMoney=true (P2-C: RenderMoneyMode was built but financeDrawFunc never called it)")
	}
}

// TestFinanceDrawFunc_RealModeShowsPLNotIndicator is the paired control:
// a screen that published unlimitedMoney=false draws the ordinary P&L
// heading and NOT the Unlimited Money indicator banner, and the 2x2 grid
// starts at its ORIGINAL top row (no leftover gap from an indicator strip
// this session never activated) -- the composed draw closure must not
// show the sandbox indicator, or shift the grid, in Real mode.
func TestFinanceDrawFunc_RealModeShowsPLNotIndicator(t *testing.T) {
	const w, h = 100, 40

	fs := financescreen.New("t-financedraw-real")
	fs.BindSubscription("sub-1")
	fs.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1, "unlimitedMoney": false}`),
	})

	buf := core.NewBuffer(w, h)
	draw := financeDrawFunc(fs)
	draw(buf, nil)

	if bufferContainsString(buf, w, h, "UNLIMITED MONEY") {
		t.Fatalf("financeDrawFunc composed output drew the Unlimited Money indicator banner while the screen published unlimitedMoney=false -- Real mode must never show the sandbox indicator")
	}
	gridY := screenContentRect(buf).Y
	if !bufferContainsString(buf, w, h, "PROFIT & LOSS") || bufferRowString(buf, w, gridY) == "" {
		t.Fatalf("expected RenderPL's \"PROFIT & LOSS\" heading somewhere in the output in Real mode; found none")
	}
	if !containsAt(bufferRowString(buf, w, gridY), "PROFIT & LOSS") {
		t.Fatalf("RenderPL's heading is not at the grid's original top row %d -- the indicator strip's absence must not shift the grid down (moneyModeRows must be 0 when unlimited=false)", gridY)
	}
}

func containsAt(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
