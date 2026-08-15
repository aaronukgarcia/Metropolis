package ticker

// Ticker-scroll motion (TIK-1): the rolling ticker advances through its
// atomic events with a deterministic scroll — a pure function of the
// current scroll step and the number of events, never the wall clock
// (SF-8/GR#21). UI-SPEC §2 names "ticker scroll" as one of the three
// permitted motions alongside queue growth and the 300ms pulse; unlike
// queue growth (widgets.QueueLane) and the pulse (widgets.PulseState),
// ui.widgets carries no shared ticker-scroll primitive today, so the
// deterministic step model is implemented here, driven by caller-supplied
// steps exactly the way widgets.PulseState is driven by caller ticks
// rather than an internally sampled clock (see doc.go's ASM note for the
// migration-debt flag).

// scrollPosition returns the index of the event that should sit at the
// top of the visible ticker window, given the current scroll step and the
// number of ticker events. It is a pure, deterministic function: it
// wraps, so a scroll past the end of the list rolls back to the start
// (the rolling-ticker idiom), and it is non-negative for any non-negative
// step. step is kept non-negative by the caller (Screen.AdvanceScroll
// clamps); this function still handles a negative step defensively so a
// misbehaving caller degrades to the wrapped equivalent rather than a
// negative slice index.
func scrollPosition(step, n int) int {
	if n <= 0 {
		return 0
	}
	p := step % n
	if p < 0 {
		p += n
	}
	return p
}

// window returns the slice of events visible in a window of size w rows
// starting at scroll offset start, wrapping through the list. It is pure:
// the same (events, start, w) always produces the same window (GR#21),
// and the result is a fresh slice, never a sub-slice alias of events (so
// a caller mutating it cannot corrupt the screen's stored state). When w
// exceeds len(events), the whole list is returned exactly once (no
// duplicate rows from wrapping a window larger than the data).
func window(events []Story, start, w int) []Story {
	n := len(events)
	if n == 0 || w <= 0 {
		return nil
	}
	if w > n {
		w = n
	}
	out := make([]Story, 0, w)
	for i := 0; i < w; i++ {
		out = append(out, events[(start+i)%n])
	}
	return out
}
