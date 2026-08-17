package capexport

import (
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/engine/finance"
)

// TestContractTermAndCancellationPenalty (AC-4): a contract is a real
// commitment — a term, a per-unit rate, and a cancellation-penalty function —
// not a boolean toggle. The three fields are independently retrievable, and the
// penalty function is nonzero before term-end and exactly zero at/after
// term-end.
func TestContractTermAndCancellationPenalty(t *testing.T) {
	a, svc, _, _ := newTestAPI(t)
	id := registerService(t, svc, "hospital", 100)
	setDemand(t, svc, id, 0)
	bindLine(t, a, ExportHospitalBeds, id)

	c, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 2, TermMonths: 12, RateMicropounds: 5_000_000})
	if err != nil {
		t.Fatalf("IssueContract: %v", err)
	}

	// All three fields independently retrievable from the durable record.
	got, ok := a.Contract(c.ID)
	if !ok {
		t.Fatalf("Contract(%d) not found", c.ID)
	}
	if got.TermMonths != 12 {
		t.Fatalf("TermMonths = %d, want 12", got.TermMonths)
	}
	if got.RateMicropounds != 5_000_000 {
		t.Fatalf("RateMicropounds = %d, want 5000000", got.RateMicropounds)
	}
	if got.Quantity != 2 {
		t.Fatalf("Quantity = %v, want 2", got.Quantity)
	}

	// The penalty is a function of remaining term: 12 months remaining at
	// issue (month 0), so the full remaining revenue is forfeit.
	if p, err := c.CancellationPenalty(0); err != nil || p != 12*5_000_000*2 {
		t.Fatalf("CancellationPenalty(0) = %d, %v; want %d, nil", p, err, 12*5_000_000*2)
	}
	if p, err := c.CancellationPenalty(12); err != nil || p != 0 {
		t.Fatalf("CancellationPenalty(12) = %d, %v; want 0, nil (at term-end)", p, err)
	}
	if p, err := c.CancellationPenalty(30); err != nil || p != 0 {
		t.Fatalf("CancellationPenalty(30) = %d, %v; want 0, nil (after term-end)", p, err)
	}
}

// TestCancellationPenaltyPostsOnlyBeforeTermEnd (AC-4, posted path): cancelling
// before term-end posts a nonzero penalty; cancelling at/after term-end posts
// none.
func TestCancellationPenaltyPostsOnlyBeforeTermEnd(t *testing.T) {
	t.Run("before term-end", func(t *testing.T) {
		a, svc, fin, _ := newTestAPI(t)
		id := registerService(t, svc, "hospital", 100)
		bindLine(t, a, ExportHospitalBeds, id)
		c, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 2, TermMonths: 12, RateMicropounds: 5_000_000})
		if err != nil {
			t.Fatalf("IssueContract: %v", err)
		}
		canc, err := a.PayCancellationPenalty(c.ID)
		if err != nil {
			t.Fatalf("PayCancellationPenalty: %v", err)
		}
		if canc.Penalty != 12*5_000_000*2 {
			t.Fatalf("penalty = %d, want %d (before term-end)", canc.Penalty, 12*5_000_000*2)
		}
		if !hasTreasurySide(fin, canc.Penalty, finance.SideDebit) {
			t.Fatalf("penalty not posted as a treasury debit")
		}
	})

	t.Run("at term-end", func(t *testing.T) {
		a, svc, _, _ := newTestAPI(t)
		id := registerService(t, svc, "hospital", 100)
		bindLine(t, a, ExportHospitalBeds, id)
		if err := a.SetMonth(12); err != nil {
			t.Fatalf("SetMonth: %v", err)
		}
		c, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 2, TermMonths: 12, RateMicropounds: 5_000_000})
		if err != nil {
			t.Fatalf("IssueContract: %v", err)
		}
		// Advance to exactly the term end (issued month 12 + term 12 = 24).
		if err := a.SetMonth(24); err != nil {
			t.Fatalf("SetMonth: %v", err)
		}
		canc, err := a.PayCancellationPenalty(c.ID)
		if err != nil {
			t.Fatalf("PayCancellationPenalty: %v", err)
		}
		if canc.Penalty != 0 {
			t.Fatalf("penalty at term-end = %d, want 0", canc.Penalty)
		}
	})
}

// TestContractRatesVaryIndependently (AC-4's "vary independently across test
// fixtures"): two contracts on the same line carry independent rates — a
// contract is not a single line-wide boolean.
func TestContractRatesVaryIndependently(t *testing.T) {
	a, svc, _, _ := newTestAPI(t)
	id := registerService(t, svc, "hospital", 1000)
	bindLine(t, a, ExportHospitalBeds, id)

	c1, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 1, TermMonths: 6, RateMicropounds: 1_000_000})
	if err != nil {
		t.Fatalf("IssueContract c1: %v", err)
	}
	c2, err := a.IssueContract(IssueRequest{Line: ExportHospitalBeds, Quantity: 1, TermMonths: 6, RateMicropounds: 9_000_000})
	if err != nil {
		t.Fatalf("IssueContract c2: %v", err)
	}
	if c1.RateMicropounds == c2.RateMicropounds {
		t.Fatalf("two contracts share a rate (%d) — rates must vary independently per contract", c1.RateMicropounds)
	}
}
