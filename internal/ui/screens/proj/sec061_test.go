package proj

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestCurveLabelLine_BoundedByDrawnWidth is SEC-061's regression: the
// curve label must be bounded by the drawn width, never by the number of
// queued-decision markers. A hostile "f7.projections" patch can carry ~58k
// markers and still fit the 1 MiB wire cap; the pre-fix curveLabelLine
// built the label with `s += fmt.Sprintf(...)` per marker (O(n²) string
// copies — measured at 5.14s for 60k markers, run on the render goroutine
// every tick). The fix builds with a strings.Builder and stops before a
// marker note that would push the row past maxWidth, so the returned label
// is at most maxWidth runes regardless of marker count: the work is bounded
// by the drawn width, not the (attacker-influenced) input size.
func TestCurveLabelLine_BoundedByDrawnWidth(t *testing.T) {
	const maxWidth = 40
	c := Curve{
		Key:     "water.demand",
		Markers: make([]DecisionMarker, 30000),
	}
	line := curveLabelLine(c, maxWidth)
	if !strings.HasPrefix(line, "water.demand") {
		t.Errorf("curveLabelLine dropped the key prefix: got %q", line)
	}
	if r := utf8.RuneCountInString(line); r > maxWidth {
		t.Errorf("curveLabelLine with 30000 markers and width %d produced a %d-rune label, want <= %d (the label build must be bounded by the drawn width, not the marker count)", maxWidth, r, maxWidth)
	}
}
