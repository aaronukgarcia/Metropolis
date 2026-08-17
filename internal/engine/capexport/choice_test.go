package capexport

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// newCrossingState builds the AC-3 crossing state in the order §36 describes —
// sell the slack first, then let internal demand grow into it: capacity 100,
// demand 20 when the 30-unit contract is signed (headroom 70), then demand
// driven up to 80 so headroom (100 − 30 = 70) is below demand (80) — a
// crossing with shortfall 10.
func newCrossingState(t *testing.T) (*CapExportAPI, *finance.FinanceAPI, ContractID) {
	t.Helper()
	a, svc, fin, _ := newTestAPI(t)
	id := registerService(t, svc, "hospital", 100)
	setDemand(t, svc, id, 20)
	bindLine(t, a, ExportHospitalBeds, id)
	c, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 30, TermMonths: 12, RateMicropounds: 1_000_000})
	if err != nil {
		t.Fatalf("IssueContract: %v", err)
	}
	setDemand(t, svc, id, 80) // internal demand grows into the sold capacity
	return a, fin, c.ID
}

// TestPenaltyVsServiceCut (AC-3): from the same crossing state, the two paths
// are mutually exclusive and each produces the OTHER's absence.
//
// The crossing state is capacity 100, internal demand 80, committed 30 —
// headroom 70 < demand 80, a crossing with shortfall 10. Citizens' baseline
// coverage (min(demand, capacity)) is 80.
//
//   - PayCancellationPenalty posts the penalty (trade-tagged debit), cancels
//     the contract, and leaves citizens' coverage UNCHANGED (still 80) — it
//     records no cut.
//   - CutInternalService keeps the contract intact, posts no penalty, records
//     a cut, and DROPS citizens' coverage by the shortfall (80 → 70).
func TestPenaltyVsServiceCut(t *testing.T) {
	t.Run("pay cancellation penalty leaves coverage unchanged", func(t *testing.T) {
		a, fin, cid := newCrossingState(t)

		state, err := a.Crossing(ExportHospitalBeds)
		if err != nil {
			t.Fatalf("Crossing: %v", err)
		}
		if !state.Crossing || state.Shortfall != 10 {
			t.Fatalf("crossing state = %+v, want Crossing=true Shortfall=10", state)
		}

		before, err := a.CitizenCoverage(ExportHospitalBeds)
		if err != nil {
			t.Fatalf("CitizenCoverage (before): %v", err)
		}

		canc, err := a.PayCancellationPenalty(cid)
		if err != nil {
			t.Fatalf("PayCancellationPenalty: %v", err)
		}
		if canc.Penalty <= 0 {
			t.Fatalf("penalty = %v, want positive (before term-end)", canc.Penalty)
		}

		// The contract is cancelled.
		c, ok := a.Contract(cid)
		if !ok || !c.Cancelled {
			t.Fatalf("contract after penalty = %+v (ok=%v), want cancelled", c, ok)
		}

		// Citizens' coverage is genuinely UNCHANGED — the penalty does not move
		// the figure the cut would have reduced.
		after, err := a.CitizenCoverage(ExportHospitalBeds)
		if err != nil {
			t.Fatalf("CitizenCoverage (after): %v", err)
		}
		if after != before {
			t.Fatalf("citizen coverage changed %v -> %v on penalty, want unchanged", before, after)
		}

		// No service cut was recorded — the two paths are mutually exclusive.
		if _, ok := a.Cut(ExportHospitalBeds); ok {
			t.Fatalf("penalty path must not record a service cut")
		}

		// The penalty reached the trade ledger as a debit.
		if !hasTreasurySide(fin, canc.Penalty, finance.SideDebit) {
			t.Fatalf("penalty %v not found as a treasury debit in the trade ledger", canc.Penalty)
		}
	})

	t.Run("cut internal service drops coverage by the shortfall", func(t *testing.T) {
		a, fin, cid := newCrossingState(t)

		before, err := a.CitizenCoverage(ExportHospitalBeds)
		if err != nil {
			t.Fatalf("CitizenCoverage (before): %v", err)
		}

		cut, err := a.CutInternalService(ExportHospitalBeds)
		if err != nil {
			t.Fatalf("CutInternalService: %v", err)
		}
		if cut.Shortfall != 10 {
			t.Fatalf("cut shortfall = %v, want 10", cut.Shortfall)
		}

		// The contract is intact — honouring the commitment is the whole point.
		c, ok := a.Contract(cid)
		if !ok || c.Cancelled {
			t.Fatalf("contract after cut = %+v (ok=%v), want intact (not cancelled)", c, ok)
		}

		// Citizens' coverage dropped by exactly the shortfall. A no-op cut would
		// leave the coverage identical, so this assertion FAILS against a cut
		// that merely records a record without reducing coverage.
		after, err := a.CitizenCoverage(ExportHospitalBeds)
		if err != nil {
			t.Fatalf("CitizenCoverage (after): %v", err)
		}
		if drop := before - after; drop != cut.Shortfall {
			t.Fatalf("coverage dropped by %v, want shortfall %v (a no-op cut would drop 0)", drop, cut.Shortfall)
		}
		if after != 70 {
			t.Fatalf("citizen coverage after cut = %v, want 70 (baseline 80 − shortfall 10)", after)
		}

		// No penalty posted — the cut path posts nothing to the ledger.
		if got := fin.LinesByCategory(CatTradeExport); len(got) != 0 {
			t.Fatalf("cut path posted %d trade-ledger entries, want 0 (no penalty)", len(got))
		}

		// The cut is durably queryable.
		if got, ok := a.Cut(ExportHospitalBeds); !ok || got.Shortfall != 10 {
			t.Fatalf("recorded cut = %+v (ok=%v), want shortfall 10", got, ok)
		}
	})
}

// hasTreasurySide reports whether the trade ledger holds a treasury entry of
// exactly amount on the given side.
func hasTreasurySide(fin *finance.FinanceAPI, amount finance.Money, side finance.Side) bool {
	for _, e := range fin.LinesByCategory(CatTradeExport) {
		if e.Account == finance.AcctTreasury && e.Amount == amount && e.Side == side {
			return true
		}
	}
	return false
}

// TestCutInternalServiceRequiresCrossing (AC-3 boundary): without a crossing
// there is nothing to cut, so CutInternalService rejects rather than silently
// cutting citizens by zero.
func TestCutInternalServiceRequiresCrossing(t *testing.T) {
	a, svc, _, _ := newTestAPI(t)
	id := registerService(t, svc, "hospital", 100)
	setDemand(t, svc, id, 10) // well below any committed headroom
	bindLine(t, a, ExportHospitalBeds, id)
	if _, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 30, TermMonths: 12, RateMicropounds: 1_000_000}); err != nil {
		t.Fatalf("IssueContract: %v", err)
	}

	if _, err := a.CutInternalService(ExportHospitalBeds); err == nil {
		t.Fatalf("CutInternalService without a crossing returned nil, want ErrNoCrossing")
	}
}
