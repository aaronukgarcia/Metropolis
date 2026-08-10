package replay

import (
	"fmt"

	"github.com/aaronukgarcia/Metropolis/internal/protocol"
)

// CompareResult is the outcome of comparing a fixture's recorded
// CommandResult stream against the stream a live replay actually
// produced (AC-5) — a pass/fail diff, never just "replayed without
// crashing". It is a pure function of its two inputs: replaying the
// same fixture twice against the same engine build/seed produces
// byte-for-byte identical Diffs both times (AC-11), since nothing here
// reads the wall clock or depends on goroutine scheduling.
type CompareResult struct {
	// Matched is true iff Diffs is empty.
	Matched bool
	// Diffs is one human-readable line per divergence found, empty when
	// Matched is true.
	Diffs []string
}

// compareResults builds a CompareResult from want (the fixture's
// recorded results) and got (what a live replay actually produced), in
// index order. A length mismatch is reported once; every index present
// in both is compared on CorrelationID, Accepted, error-code identity
// (both nil, or both non-nil with the same Code — Display text is
// deliberately excluded, since it may legitimately vary run-to-run,
// e.g. an embedded correlation ID), and Tick.
func compareResults(want, got []protocol.CommandResult) *CompareResult {
	var diffs []string
	if len(want) != len(got) {
		diffs = append(diffs, fmt.Sprintf("result count mismatch: fixture recorded %d, replay produced %d", len(want), len(got)))
	}

	n := len(want)
	if len(got) < n {
		n = len(got)
	}
	for i := 0; i < n; i++ {
		w, g := want[i], got[i]
		if diff := diffOneResult(i, w, g); diff != "" {
			diffs = append(diffs, diff)
		}
	}
	return &CompareResult{Matched: len(diffs) == 0, Diffs: diffs}
}

func diffOneResult(i int, w, g protocol.CommandResult) string {
	if w.CorrelationID == g.CorrelationID && w.Accepted == g.Accepted && w.Tick == g.Tick && sameErrorCode(w.Error, g.Error) {
		return ""
	}
	return fmt.Sprintf(
		"result[%d]: fixture={correlation:%s accepted:%v tick:%d error:%s} replay={correlation:%s accepted:%v tick:%d error:%s}",
		i, w.CorrelationID, w.Accepted, w.Tick, errorCodeOrNone(w.Error),
		g.CorrelationID, g.Accepted, g.Tick, errorCodeOrNone(g.Error),
	)
}

func sameErrorCode(a, b *protocol.ErrorRef) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return a.Code == b.Code
}

func errorCodeOrNone(e *protocol.ErrorRef) string {
	if e == nil {
		return "<none>"
	}
	return e.Code
}
