package widgets

import (
	"strings"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// gridRunes renders buf's rune grid as one string per row, for readable
// snapshot-test failures (a raw Cell-by-Cell diff is unreadable; a
// row of runes matches what a human sees on screen). Out-of-range/zero
// runes (should never occur for an in-bounds NewBuffer, which
// blank-fills with ' ') render as ' ' defensively.
func gridRunes(buf *core.Buffer) []string {
	w, h := buf.Size()
	rows := make([]string, h)
	for y := 0; y < h; y++ {
		var sb strings.Builder
		for x := 0; x < w; x++ {
			r := buf.Get(x, y).Rune
			if r == 0 {
				r = ' '
			}
			sb.WriteRune(r)
		}
		rows[y] = sb.String()
	}
	return rows
}

// assertGrid fails t with a readable row-by-row diff if buf's rune grid
// does not exactly match want.
func assertGrid(t *testing.T, buf *core.Buffer, want []string) {
	t.Helper()
	got := gridRunes(buf)
	if len(got) != len(want) {
		t.Fatalf("row count = %d, want %d\n got: %q\nwant: %q", len(got), len(want), got, want)
	}
	for y := range want {
		if got[y] != want[y] {
			t.Errorf("row %d:\n got  %q\nwant  %q", y, got[y], want[y])
		}
	}
}
