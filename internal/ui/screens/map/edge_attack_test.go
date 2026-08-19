package mapscreen

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/widgets"
)

func TestAttack_PaintOverlay_EmptyAnd1x1Viewport(t *testing.T) {
	// empty rect
	buf := core.NewBuffer(3, 3)
	snap := renderSnapshot{width: 3, height: 3}
	get := func(ov Overlay, x, y int) (float64, bool) { return 0.5, true }
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("paintOverlay panicked on empty rect: %v", r)
			}
		}()
		paintOverlay(buf, core.Rect{X: 0, Y: 0, W: 0, H: 0}, snap, OverlayTraffic, get, 0, 1, twoStopRamp)
	}()

	// 1x1 viewport
	buf2 := core.NewBuffer(1, 1)
	buf2.Set(0, 0, 'X', tcell.StyleDefault)
	snap2 := renderSnapshot{width: 1, height: 1}
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("paintOverlay panicked on 1x1 rect: %v", r)
			}
		}()
		paintOverlay(buf2, core.Rect{X: 0, Y: 0, W: 1, H: 1}, snap2, OverlayTraffic, get, 0, 1, twoStopRamp)
	}()
	c := buf2.Get(0, 0)
	if c.Rune != 'X' {
		t.Fatalf("1x1 viewport: glyph touched, got %q", c.Rune)
	}

	// negative rect dims
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("paintOverlay panicked on negative rect: %v", r)
			}
		}()
		paintOverlay(buf, core.Rect{X: 0, Y: 0, W: -1, H: -1}, snap, OverlayTraffic, get, 0, 1, twoStopRamp)
	}()
}

func TestAttack_Render_ZeroSizeViewport(t *testing.T) {
	m := NewMapScreen("test", widgets.DefaultPalette)
	m.SetViewportSize(0, 0)
	buf := core.NewBuffer(5, 5)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Render panicked on zero-size viewport: %v", r)
		}
	}()
	m.Render(buf, core.Rect{X: 0, Y: 0, W: 5, H: 5})
}
