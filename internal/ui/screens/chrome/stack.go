package chrome

import "sort"

// lessAlerts is the total, deterministic ordering of the alert stack
// (AC-5, AC-14): higher tier first; within a tier, oldest (lowest Tick)
// first; within an equal Tick, ascending ID — a data-only tie-break so
// the ordering never depends on insertion order, Go map iteration, or any
// per-run nondeterminism. This is deliberately a pure function of two
// Alert VALUES so it is trivially testable and cannot consult hidden
// mutable state.
func lessAlerts(a, b Alert) bool {
	if a.Tier != b.Tier {
		return a.Tier > b.Tier
	}
	if a.Tick != b.Tick {
		return a.Tick < b.Tick
	}
	return a.ID < b.ID
}

// sortAlerts returns a copy of alerts sorted by lessAlerts. The copy means
// the caller's slice (and backing array) is never mutated — Alert values
// are treated as immutable once constructed (Alert's doc comment), and
// Render holds a snapshot outside the lock while AddAlert/ResolveAlert
// mutate the live slice, so nothing may alias it.
func sortAlerts(alerts []Alert) []Alert {
	out := make([]Alert, len(alerts))
	copy(out, alerts)
	sort.SliceStable(out, func(i, j int) bool { return lessAlerts(out[i], out[j]) })
	return out
}

// insertSortedLocked appends a to alerts and re-sorts the result in place,
// keeping the stack ordered by lessAlerts. Caller must hold c.mu. It does
// not deduplicate — the same ID may appear once per distinct ingest; the
// crisis dedupe (seenCrisis) is a separate concern from stack membership
// (see AddAlert).
func insertSortedLocked(alerts []Alert, a Alert) []Alert {
	alerts = append(alerts, a)
	sort.SliceStable(alerts, func(i, j int) bool { return lessAlerts(alerts[i], alerts[j]) })
	return alerts
}

// removeByIDLocked returns a copy of alerts with every entry whose ID
// equals id dropped (AC-12's resolution). Ordering of the survivors is
// preserved. Caller must hold c.mu.
func removeByIDLocked(alerts []Alert, id string) []Alert {
	out := make([]Alert, 0, len(alerts))
	for _, a := range alerts {
		if a.ID == id {
			continue
		}
		out = append(out, a)
	}
	return out
}

// snapshotAlerts returns a defensive copy of alerts (new backing array) so
// a lock-free reader (Render) can never alias the live slice a concurrent
// AddAlert/ResolveAlert is mutating (AC-16's -race requirement).
func snapshotAlerts(alerts []Alert) []Alert {
	out := make([]Alert, len(alerts))
	copy(out, alerts)
	return out
}
