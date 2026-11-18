package flyout

import (
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
	"github.com/aaronukgarcia/Metropolis/internal/ui/keys"
)

// act, sub and grey build small fixture trees; no menu lives in the package.
func act(label string, letter rune, id string) Item {
	return Item{Label: label, Letter: letter, Kind: KindAction, ActionID: id}
}

func actR(label string, letter rune, id, right string) Item {
	it := act(label, letter, id)
	it.Right = right
	return it
}

func sub(label string, letter rune, items ...Item) Item {
	return Item{Label: label, Letter: letter, Kind: KindSubmenu, Items: items}
}

func grey(label string, letter rune, reason string) Item {
	return Item{Label: label, Letter: letter, Kind: KindAction, Grey: reason}
}

var sep = Item{Kind: KindSeparator}

// sampleItems is a three-level fixture shaped like the real menu: Build >
// Housing > Dwelling is three picks deep.
func sampleItems() []Item {
	return []Item{
		sub("Build", 'b',
			sub("Housing", 'h',
				actR("Dwelling", 'd', "build:dwelling", "£15,000  8 homes"),
				actR("Flats", 'f', "build:flats", "£40,000  24 homes"),
			),
			func() Item {
				j := sub("Jobs", 'j',
					grey("Office", 'o', "not active yet"),
					grey("Farm", 'f', "not active yet"),
				)
				j.Grey = "not active yet"
				return j
			}(),
			sep,
			act("Buy this cell", 'y', "cmd:buy"),
			act("Demolish here", 'r', "cmd:demolish"),
		),
		sub("Screens", 's',
			actR("Map", 'm', "screen:map", "F1"),
			actR("Finance", 'f', "screen:finance", "F2"),
			grey("Save city", 'v', "no engine command yet"),
		),
		sub("Game", 'g',
			actR("Pause", 'p', "game:pause", "Space"),
			sep,
			actR("Quit", 'q', "game:quit", "q"),
		),
	}
}

func mustTable(t *testing.T, items []Item) *Table {
	t.Helper()
	tb, err := NewTable(items)
	if err != nil {
		t.Fatalf("NewTable: %v", err)
	}
	return tb
}

func sampleMenu(t *testing.T) *Menu { return New(mustTable(t, sampleItems())) }

// k turns a token into a key: "Down", "Esc", or a single rune.
func k(tok string) keys.Key {
	if len([]rune(tok)) == 1 {
		return keys.KeyRune([]rune(tok)[0])
	}
	return keys.Key{Special: tok}
}

// run feeds tokens in order and returns the last result.
func run(m *Menu, toks ...string) Result {
	var r Result
	for _, tok := range toks {
		r = m.Press(k(tok))
	}
	return r
}

// rows returns the buffer as text rows (trailing spaces kept, so a frame
// compares exactly).
func rows(buf *core.Buffer) []string {
	w, h := buf.Size()
	out := make([]string, h)
	for y := 0; y < h; y++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			r := buf.Get(x, y).Rune
			if r == 0 {
				r = ' '
			}
			b.WriteRune(r)
		}
		out[y] = b.String()
	}
	return out
}

// mapBuffer returns a w x h buffer full of map glyphs ('#').
func mapBuffer(w, h int) *core.Buffer {
	b := core.NewBuffer(w, h)
	b.Fill('#', b.Get(0, 0).Style)
	return b
}

func fullArea(w, h int) core.Rect { return core.Rect{X: 0, Y: 0, W: w, H: h} }

// inside reports whether (x, y) is in r.
func inside(r core.Rect, x, y int) bool {
	return x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}
