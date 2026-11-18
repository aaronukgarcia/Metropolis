package flyout

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func TestRoundR3_ZeroAllocs(t *testing.T) {
	for _, sz := range [][2]int{{120, 30}, {60, 20}, {40, 12}, {30, 8}} {
		m := r3FourLevel(t)
		buf := core.NewBuffer(sz[0], sz[1])
		a := core.Rect{W: sz[0], H: sz[1]}
		down, up := k("Down"), k("Up")
		i := 0
		n := testing.AllocsPerRun(200, func() {
			if i%2 == 0 {
				m.Press(down)
			} else {
				m.Press(up)
			}
			i++
			m.Draw(buf, a)
			lay := m.Layout(a)
			_ = lay.Hit(5, 5)
		})
		if n != 0 {
			t.Fatalf("size %v: %v allocations per Press+Draw", sz, n)
		}
	}
}
