package finance

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/gdamore/tcell/v2"
)

// TestUnlimitedMoney_Signal (AC-7): the screen's patch/state carries the
// mode's derived "unlimited" flag, distinct in the two modes.
func TestUnlimitedMoney_Signal(t *testing.T) {
	s := New("corr-unlimited")
	s.BindSubscription("sub-1")

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1, "unlimitedMoney": true}`),
	})
	got, have := s.UnlimitedMoney()
	if !have || !got {
		t.Fatalf("after unlimitedMoney:true patch: UnlimitedMoney() = (%v, %v), want (true, true)", got, have)
	}

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1, "unlimitedMoney": false}`),
	})
	got, have = s.UnlimitedMoney()
	if !have || got {
		t.Fatalf("after unlimitedMoney:false patch: UnlimitedMoney() = (%v, %v), want (false, true)", got, have)
	}
}

// TestUnlimitedMoney_AbsentClearsHaveFlag mirrors every other optional
// section: a cycle with no unlimitedMoney key clears the have-flag rather
// than keeping a stale prior value.
func TestUnlimitedMoney_AbsentClearsHaveFlag(t *testing.T) {
	s := New("corr-unlimited-absent")
	s.BindSubscription("sub-1")

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1, "unlimitedMoney": true}`),
	})
	if _, have := s.UnlimitedMoney(); !have {
		t.Fatalf("precondition: expected have=true after first patch")
	}

	s.ApplyDelta(protocol.Delta{
		SubscriptionID: "sub-1",
		Patch:          []byte(`{"schemaVersion": 1}`),
	})
	if _, have := s.UnlimitedMoney(); have {
		t.Fatalf("after a patch with no unlimitedMoney key: have = true, want false (cleared, not stale)")
	}
}

// TestRenderMoneyMode_UnlimitedShowsIndicator (AC-7 e2e): rendering in
// Unlimited mode draws the infinite indicator.
func TestRenderMoneyMode_UnlimitedShowsIndicator(t *testing.T) {
	buf := core.NewBuffer(60, 4)
	rect := core.Rect{X: 0, Y: 0, W: 60, H: 4}
	style := tcell.StyleDefault

	RenderMoneyMode(buf, rect, true, true, style)

	found := false
	for x := 0; x < 60; x++ {
		if buf.Get(x, 0).Rune == 'U' {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("RenderMoneyMode(unlimited=true) drew nothing at row 0, want the indicator text")
	}
}

// TestRenderMoneyMode_RealDrawsNothing (AC-7: the finance UI never lies —
// Real mode must not show the infinite indicator, leaving the ordinary
// budget-constrained P&L/balance surfaces as the only display).
func TestRenderMoneyMode_RealDrawsNothing(t *testing.T) {
	buf := core.NewBuffer(60, 4)
	rect := core.Rect{X: 0, Y: 0, W: 60, H: 4}
	style := tcell.StyleDefault

	RenderMoneyMode(buf, rect, false, true, style)

	for y := 0; y < 4; y++ {
		for x := 0; x < 60; x++ {
			if r := buf.Get(x, y).Rune; r != ' ' {
				t.Fatalf("RenderMoneyMode(unlimited=false) drew %q at (%d,%d), want nothing", r, x, y)
			}
		}
	}
}
