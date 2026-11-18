package flyout

import (
	"fmt"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

func bigList(n int) []Item {
	var many []Item
	for i := 0; i < n; i++ {
		many = append(many, Item{Label: fmt.Sprintf("Row %d", i), Letter: rune(0x4e00 + i), Kind: KindAction, ActionID: fmt.Sprintf("r%d", i)})
	}
	return []Item{sub("Many", 'm', many...), sub("Two", 't', act("A", 'a', "a"))}
}

func TestRoundR1_ZeroAllocAndFlat(t *testing.T) {
	var visits [2]int
	for i, n := range []int{40, 10000} {
		m := New(mustTable(t, bigList(n)))
		m.Open()
		buf := core.NewBuffer(120, 30)
		a := fullArea(120, 30)
		kd, ku := k("Down"), k("Up")
		allocs := testing.AllocsPerRun(50, func() {
			m.Press(kd)
			m.Draw(buf, a)
			m.Press(ku)
			m.Draw(buf, a)
		})
		if allocs != 0 {
			t.Errorf("n=%d allocs %v", n, allocs)
		}
		m.ResetVisits()
		m.Press(kd)
		m.Draw(buf, a)
		visits[i] = m.Visits()
		// End on the big list
		m.Press(k("End"))
		m.Draw(buf, a)
		for _, r := range rows(buf)[:4] {
			t.Log(r)
		}
	}
	if visits[0] != visits[1] {
		t.Errorf("visits grow with list length: %v", visits)
	}
}

func TestRoundR1_ThreeLevelFrame(t *testing.T) {
	m := New(mustTable(t, wideTree()))
	m.Open()
	run(m, "a", "Enter")
	m.Press(k("Down"))
	run(m, "Right", "Right")
	buf := core.NewBuffer(120, 30)
	m.Draw(buf, fullArea(120, 30))
	for _, r := range rows(buf)[:12] {
		t.Log("|" + r + "|")
	}
}
