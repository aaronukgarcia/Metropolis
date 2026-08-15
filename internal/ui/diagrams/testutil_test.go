package diagrams

import (
	"strings"

	"github.com/aaronukgarcia/Metropolis/internal/ui/core"
)

// gridRows renders buf's rune grid as one string per row, for readable
// snapshot-test failures. Out-of-range/zero runes render as ' ' defensively.
func gridRows(buf *core.Buffer) []string {
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

// bufferContains reports whether any row of buf contains s contiguously.
func bufferContains(buf *core.Buffer, s string) bool {
	for _, row := range gridRows(buf) {
		if strings.Contains(row, s) {
			return true
		}
	}
	return false
}

// bufferEqual reports whether two buffers are byte-identical (same size,
// same rune and style in every cell).
func bufferEqual(a, b *core.Buffer) bool {
	aw, ah := a.Size()
	bw, bh := b.Size()
	if aw != bw || ah != bh {
		return false
	}
	for y := 0; y < ah; y++ {
		for x := 0; x < aw; x++ {
			ca, cb := a.Get(x, y), b.Get(x, y)
			if ca.Rune != cb.Rune || ca.Style != cb.Style {
				return false
			}
		}
	}
	return true
}

// hitsEqual reports whether two hit lists are identical, order included.
func hitsEqual(a, b []Hit) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// hitIDs returns the set of SourceIDs in hits.
func hitIDs(hits []Hit) map[SourceID]bool {
	out := make(map[SourceID]bool, len(hits))
	for _, h := range hits {
		out[h.ID] = true
	}
	return out
}
