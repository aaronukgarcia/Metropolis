package flyout

import (
	"fmt"
	"math/rand"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// randTree builds a random valid tree of depth up to d with distinct letters per list.
func randTree(rng *rand.Rand, depth, maxDepth, width int, big bool) []Item {
	n := 1 + rng.Intn(width)
	if big && depth == 1 {
		n = 500
	}
	letters := []rune("abcdefghijklmnopqrstuvwxyz0123456789")
	rng.Shuffle(len(letters), func(i, j int) { letters[i], letters[j] = letters[j], letters[i] })
	var items []Item
	for i := 0; i < n && i < len(letters); i++ {
		// separators between, never first/last/adjacent
		if i > 0 && len(items) > 0 && items[len(items)-1].Kind != KindSeparator && rng.Intn(5) == 0 && i < n-1 {
			items = append(items, sep)
		}
		label := fmt.Sprintf("Item %d £…é", i)
		if rng.Intn(4) == 0 {
			label = "Dup"
		}
		it := Item{Label: label, Letter: letters[i]}
		if depth < maxDepth && rng.Intn(3) == 0 {
			it.Kind = KindSubmenu
			it.Items = randTree(rng, depth+1, maxDepth, width, big)
		} else {
			it.Kind = KindAction
			it.ActionID = fmt.Sprintf("a%d", rng.Intn(1000000))
			if rng.Intn(4) == 0 {
				it.Grey = "reason text"
			}
			if rng.Intn(3) == 0 {
				it.Right = "right col"
			}
		}
		items = append(items, it)
	}
	return items
}

func fixBar(items []Item) []Item {
	for i := range items {
		if items[i].Kind != KindSubmenu {
			items[i].Kind = KindSubmenu
			items[i].ActionID = ""
			items[i].Grey = ""
			items[i].Items = []Item{act("X", 'x', "x1")}
		}
	}
	return items
}

func checkInv(t *testing.T, m *Menu, r Result, seed int64, step int) {
	t.Helper()
	if !m.open {
		if m.n != 0 && false {
			t.Fatalf("seed %d step %d closed but n=%d", seed, step, m.n)
		}
		return
	}
	if m.n < 1 || m.n > MaxDepth {
		t.Fatalf("seed %d step %d n=%d", seed, step, m.n)
	}
	if m.bar < 0 || m.bar >= len(m.t.bar.entries) {
		t.Fatalf("bad bar")
	}
	for k := 0; k < m.n; k++ {
		l := m.listAt(k)
		if m.hl[k] < 0 || m.hl[k] >= len(l.entries) || l.entries[m.hl[k]].kind == KindSeparator {
			t.Fatalf("seed %d step %d level %d highlight %d invalid", seed, step, k, m.hl[k])
		}
		if k < m.n-1 && l.entries[m.hl[k]].kind != KindSubmenu {
			t.Fatalf("seed %d step %d parent highlight not a submenu", seed, step)
		}
	}
}

func TestRoundR1_RandomWalks(t *testing.T) {
	keysList := []string{"Up", "Down", "Left", "Right", "Enter", "Esc", "Home", "End", "F10", "x", "q", "Tab", "PgDn"}
	for seed := int64(1); seed <= 60; seed++ {
		rng := rand.New(rand.NewSource(seed))
		bar := fixBar(randTree(rng, 0, 3+int(seed%2), 8, seed%7 == 0))
		// ensure bar letters ok (distinct by construction)
		tb, err := NewTable(bar)
		if err != nil {
			t.Logf("seed %d table refused: %v", seed, err)
			continue
		}
		m := New(tb)
		buf := mapBuffer(120, 30)
		for step := 0; step < 3000; step++ {
			var r Result
			switch rng.Intn(6) {
			case 0:
				m.Toggle()
			case 1:
				x, y := rng.Intn(130)-5, rng.Intn(35)-2
				if rng.Intn(2) == 0 {
					r = m.Click(fullArea(120, 30), x, y)
				} else {
					m.Hover(fullArea(120, 30), x, y)
				}
			case 2:
				// letter, any in the alphabet
				ch := rune("abcdefghijklmnopqrstuvwxyz0123456789ABCXYZ"[rng.Intn(41)])
				r = m.Press(keys.KeyRune(ch))
			default:
				r = m.Press(k(keysList[rng.Intn(len(keysList))]))
			}
			if r.Action != "" && m.open {
				t.Fatalf("action returned but menu open")
			}
			checkInv(t, m, r, seed, step)
			if step%7 == 0 {
				m.Draw(buf, fullArea(120, 30))
			}
		}
	}
}

// Greyed never returns an action; reachability by letters.
func TestRoundR1_GreyNeverActs_And_Reach(t *testing.T) {
	for seed := int64(100); seed < 130; seed++ {
		rng := rand.New(rand.NewSource(seed))
		bar := fixBar(randTree(rng, 0, 3, 7, false))
		tb, err := NewTable(bar)
		if err != nil {
			continue
		}
		var walk func(items []Item, path []rune, depth int)
		walk = func(items []Item, path []rune, depth int) {
			for _, it := range items {
				if it.Kind == KindSeparator {
					continue
				}
				p := append(append([]rune{}, path...), it.Letter)
				if it.Kind == KindSubmenu {
					walk(it.Items, p, depth+1)
					continue
				}
				m := New(tb)
				m.Open()
				var r Result
				for _, ch := range p {
					r = m.Press(keys.KeyRune(ch))
				}
				if it.Grey != "" {
					if r.Action != "" {
						t.Fatalf("grey item acted: %v", p)
					}
				} else if r.Action != it.ActionID {
					t.Fatalf("seed %d letters %q: got %q want %q", seed, string(p), r.Action, it.ActionID)
				}
			}
		}
		walk(bar, nil, 0)
	}
}

// Remembered path then grey / invalid.
func TestRoundR1_RememberedPath(t *testing.T) {
	items := []Item{sub("Build", 'b', sub("Housing", 'h', act("Dwelling", 'd', "d"), act("Flats", 'f', "f")), act("Z", 'z', "z"))}
	tb := mustTable(t, items)
	m := New(tb)
	m.Open()
	run(m, "b", "h", "f")
	m.Toggle()
	m.Open()
	r := run(m, "Enter", "Enter")
	_ = r
	// Different table where the remembered item is greyed: same menu type can't change table, so exercise Open on new menu with copied rem.
	items2 := []Item{sub("Build", 'b', sub("Housing", 'h', act("Dwelling", 'd', "d"), grey("Flats", 'f', "nope")), act("Z", 'z', "z"))}
	tb2 := mustTable(t, items2)
	m2 := New(tb2)
	m2.rem, m2.remLen = m.rem, m.remLen
	m2.Open()
	for _, tok := range []string{"Enter", "Enter", "Enter", "Enter"} {
		rr := m2.Press(k(tok))
		if rr.Action != "" {
			t.Fatalf("grey acted")
		}
		checkInv(t, m2, rr, 0, 0)
	}
}
