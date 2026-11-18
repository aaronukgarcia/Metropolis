package finance

// MonthTransactions returns every transaction posted while the ledger was in
// the given simulation month (tx.Month == month, the months opened by
// BeginMonth), in post order. Unlike Lines/LinesByCategory, which read only
// the current tick window that the next BeginMonth discards, this reads the
// append-only transaction log, so postings made BETWEEN two monthly finance
// ticks (coastal bills, daily construction) are included once the month is
// complete (BUG-1213). Entries are copied: the caller owns the result. The
// log is persisted with the ledger, so a restored ledger answers the same.
func (f *FinanceAPI) MonthTransactions(month int64) []Transaction {
	if err := f.checkNotCopied("MonthTransactions"); err != nil {
		return nil
	}
	f.mu.RLock()
	defer f.mu.RUnlock()
	// Months never decrease along the log, so scan from the newest end and
	// stop at the first older month: the cost is the window, not the history.
	first := len(f.txns)
	for i := len(f.txns) - 1; i >= 0; i-- {
		if f.txns[i].Month < month {
			break
		}
		first = i
	}
	var out []Transaction
	for _, tx := range f.txns[first:] {
		if tx.Month != month {
			continue
		}
		cp := tx
		cp.Entries = append([]Entry(nil), tx.Entries...)
		out = append(out, cp)
	}
	return out
}
