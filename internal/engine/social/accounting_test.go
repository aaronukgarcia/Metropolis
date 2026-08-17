package social

import (
	"errors"
	"testing"

	"github.com/aaronukgarcia/Metropolis/internal/foundation/errs"
)

// TestCaseConservationIdentity (AC-11): for every category and month the
// identity
//
//	OpenCases == OpenCasesLastMonth + NewCasesOpened − CasesResolved
//	             − CasesEscalated − CasesLostToFollowUp
//
// holds exactly, with every term independently sourced from the ledger and a
// cross-category escalation appearing as BOTH a closure in the source
// category and a freshly-opened linked case in the destination category in
// the same month.
func TestCaseConservationIdentity(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Month 1: deprivation 1.0 opens 4 family-support cases.
	_ = a.AdvanceMonth(1, DriverInputs{Deprivation: 1.0})
	fam := a.OpenCaseIDs(CategoryFamilySupport)
	if len(fam) != 4 {
		t.Fatalf("expected 4 family cases, got %d", len(fam))
	}

	// Month 2: one resolution, one cross-category escalation (family →
	// fostering), one lost-to-follow-up.
	if err := a.ResolveCase(fam[0], 2, "reunited"); err != nil {
		t.Fatalf("ResolveCase: %v", err)
	}
	reopen, err := a.EscalateCase(fam[1], 2, CategoryFostering)
	if err != nil {
		t.Fatalf("EscalateCase: %v", err)
	}
	if err := a.LoseToFollowUp(fam[2], 2); err != nil {
		t.Fatalf("LoseToFollowUp: %v", err)
	}

	prev, _ := a.Accounting(CategoryFamilySupport, 1)
	cur, _ := a.Accounting(CategoryFamilySupport, 2)

	want := prev.Open + cur.Opened - cur.Resolved - cur.Escalated - cur.LostToFollowUp
	if cur.Open != want {
		t.Fatalf("family-support identity drifted: Open[2]=%d, want %d (Open[1]=%d Opened=%d Resolved=%d Escalated=%d Lost=%d)",
			cur.Open, want, prev.Open, cur.Opened, cur.Resolved, cur.Escalated, cur.LostToFollowUp)
	}

	// The escalation pairing: source closed as escalated AND destination
	// opened a linked case in the same month.
	src, err := a.Case(fam[1])
	if err != nil {
		t.Fatalf("Case(source): %v", err)
	}
	if src.Status != StatusEscalated || src.LinkedCaseID != reopen {
		t.Fatalf("source case must close as escalated and link to %d, got status=%v linked=%d", reopen, src.Status, src.LinkedCaseID)
	}
	dest, err := a.Case(reopen)
	if err != nil {
		t.Fatalf("Case(reopen): %v", err)
	}
	if dest.Category != CategoryFostering || dest.OpenedMonth != 2 || dest.LinkedCaseID != fam[1] {
		t.Fatalf("escalation must reopen in fostering at month 2 linked to source, got %+v", dest)
	}
	fos, _ := a.Accounting(CategoryFostering, 2)
	if fos.Opened != 1 || fos.Open != 1 {
		t.Fatalf("fostering must show the paired reopen as Opened=1 Open=1, got %+v", fos)
	}
}

// TestInvalidEscalation (AC-14): escalating to a destination category that
// does not exist is rejected with a typed error.
func TestInvalidEscalation(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	id, err := a.InjectCrisis(CrisisEvent{ID: "c1", Month: 1})
	if err != nil {
		t.Fatalf("InjectCrisis: %v", err)
	}
	if _, err := a.EscalateCase(id, 2, Category(99)); err == nil {
		t.Fatal("expected escalation to a nonexistent category to be rejected")
	} else if !errors.Is(err, &errs.E{Code: ErrInvalidEscalation}) {
		t.Fatalf("error code = %v, want %s", err, ErrInvalidEscalation)
	}
}

// TestDoubleClose (AC-14): resolving a case that is already closed is
// rejected with a typed error, never silently accepted.
func TestDoubleClose(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	id, err := a.InjectCrisis(CrisisEvent{ID: "c1", Month: 1})
	if err != nil {
		t.Fatalf("InjectCrisis: %v", err)
	}
	if err := a.ResolveCase(id, 2, "reunited"); err != nil {
		t.Fatalf("ResolveCase: %v", err)
	}
	if err := a.ResolveCase(id, 2, "again"); err == nil {
		t.Fatal("expected a double close to be rejected")
	} else if !errors.Is(err, &errs.E{Code: ErrDoubleClose}) {
		t.Fatalf("error code = %v, want %s", err, ErrDoubleClose)
	}
}

// TestUnknownCase (AC-13): a case query for an unregistered case id returns a
// registry-sourced error, never a zero-value case record.
func TestUnknownCase(t *testing.T) {
	a, err := New(testConfig(), 1, "test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := a.Case(CaseID(12345)); err == nil {
		t.Fatal("expected an unknown case to be rejected")
	} else if !errors.Is(err, &errs.E{Code: ErrUnknownCase}) {
		t.Fatalf("error code = %v, want %s", err, ErrUnknownCase)
	}
}
