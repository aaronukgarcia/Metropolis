package core

import "github.com/gdamore/tcell/v2"

// ScreenWriter is the subset of tcell.Screen that Flush needs. It exists
// so Flush (and its tests) never require a real or even simulated
// terminal — any type with these three methods will do, and
// tcell.Screen satisfies it as-is (no adapter needed) because Go
// interface satisfaction is structural.
//
// Notably absent: Clear and Fill. Flush never calls either — UI-SPEC §1:
// "no clears, ever" — so ScreenWriter's method set is itself a small
// piece of documentation of that invariant: it is not even possible to
// clear the screen through this interface.
type ScreenWriter interface {
	SetContent(x, y int, primary rune, combining []rune, style tcell.Style)
	Show()
}

// FlushStats reports what a Flush call actually did, for tests and for
// the perf harness (UI-SPEC §5's "full-terminal diff flush" budget).
type FlushStats struct {
	// CellsChanged is the number of cells that differed between back and
	// front and were written to the ScreenWriter.
	CellsChanged int
	// Runs is the number of contiguous same-row changed-cell runs the
	// diff found. A run of length N costs N SetContent calls (tcell has
	// no "write a run" primitive), but Runs itself is what AC-1's test
	// cares about: it should be proportional to the changed region, not
	// the screen area.
	Runs int
}

// Flush diffs back against front, writes only the cells that differ to
// w via SetContent, calls w.Show() exactly once if anything changed, and
// updates front in place to match back — the mechanism behind UI-SPEC
// §1's "flusher diffs against front and emits only changed runs."
//
// back and front must be the same size (Resize them together on
// resize — render.go does this before the next Flush). A size mismatch
// is a caller bug, not a runtime condition to recover from gracefully
// here: Flush treats it as "nothing in common" and only writes/tracks
// cells within the smaller of the two extents, rather than panicking
// T-RENDER over a transient resize race — see the two Size() checks
// below.
func Flush(w ScreenWriter, back, front *Buffer) FlushStats {
	bw, bh := back.Size()
	fw, fh := front.Size()
	width, height := bw, bh
	if fw < width {
		width = fw
	}
	if fh < height {
		height = fh
	}

	var stats FlushStats
	for y := 0; y < height; y++ {
		inRun := false
		for x := 0; x < width; x++ {
			bi, _ := back.idx(x, y)
			fi, _ := front.idx(x, y)
			bc := back.cells[bi]
			fc := front.cells[fi]
			if bc == fc {
				inRun = false
				continue
			}
			w.SetContent(x, y, bc.Rune, nil, bc.Style)
			front.cells[fi] = bc
			stats.CellsChanged++
			if !inRun {
				stats.Runs++
				inRun = true
			}
		}
	}

	if stats.CellsChanged > 0 {
		w.Show()
	}
	return stats
}
