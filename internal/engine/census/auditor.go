package census

// HistoryPoint is one auditor entry: the census's aggregates at a given
// simulation tick (US-3). The series is append-only and monotonic in tick
// order, so "how has the city changed" is a query over recorded snapshots,
// never a recomputation from memory.
type HistoryPoint struct {
	Tick       int64
	Aggregates Aggregates
}

// auditLocked appends the current aggregates to the history series keyed by
// tick (the auditor thread). Monotonic and idempotent: a tick is recorded
// only if it is strictly after the last recorded tick, so a re-audit of an
// already-seen (or out-of-order) tick never creates a duplicate or an
// out-of-order entry (AC-4). Caller holds c.mu.
func (c *CensusAPI) auditLocked(snap *Snapshot, agg Aggregates) {
	if n := len(c.history); n > 0 && c.history[n-1].Tick >= snap.Tick {
		return // duplicate or out-of-order audit: already recorded, or stale
	}
	c.history = append(c.history, HistoryPoint{Tick: snap.Tick, Aggregates: agg})
}

// HistoryAt returns the aggregates recorded at tick, and false if that
// tick has not been audited (a documented not-found result, never a silent
// zero — AC-4).
func (c *CensusAPI) HistoryAt(tick int64) (Aggregates, bool) {
	if err := c.checkNotCopied("HistoryAt"); err != nil {
		return Aggregates{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, p := range c.history {
		if p.Tick == tick {
			return p.Aggregates, true
		}
	}
	return Aggregates{}, false
}

// HistorySeries returns a defensive copy of the audited history, oldest
// first (the auditor's query surface, AC-1b).
func (c *CensusAPI) HistorySeries() []HistoryPoint {
	if err := c.checkNotCopied("HistorySeries"); err != nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return append([]HistoryPoint(nil), c.history...)
}
